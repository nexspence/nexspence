package storage

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
)

// azureBlockSize is unexported layout arithmetic; same-package test, the
// pattern used for other unexported storage helpers.
func TestAzureBlockSize(t *testing.T) {
	tests := []struct {
		name string
		size int64
		want int64
	}{
		{"unknown size defers to sdk default", -1, 0},
		{"zero size defers to sdk default", 0, 0},
		{"small blob floors at 4 MiB", 1 << 20, 4 * 1024 * 1024},
		{"mid blob still at floor", 40 << 20, 4 * 1024 * 1024},
		{"exact floor size stays at floor", 4 << 20, 4 * 1024 * 1024},
		{"huge blob caps at 256 MiB", 1 << 50, 256 * 1024 * 1024},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := azureBlockSize(tc.size); got != tc.want {
				t.Errorf("azureBlockSize(%d) = %d, want %d", tc.size, got, tc.want)
			}
		})
	}
}

// A size that sits between the floor and the cap must scale with the blob:
// exactly 49001 blocks of 4 MiB is one block over the 50k budget, so the
// block size must have grown past the floor by then.
func TestAzureBlockSize_ScalesBeforeBlockCap(t *testing.T) {
	size := int64(49001) * 4 * 1024 * 1024
	got := azureBlockSize(size)
	if got <= 4*1024*1024 {
		t.Fatalf("azureBlockSize(%d) = %d; stayed at floor, upload would exceed the 50k block cap", size, got)
	}
	if got*50000 < size {
		t.Fatalf("azureBlockSize(%d) = %d; %d blocks would not hold the blob", size, got, 50000)
	}
}

func TestAzureObjectKey_Sharding(t *testing.T) {
	s := &AzureBlobStore{}
	tests := []struct{ in, want string }{
		{"abcdef", "ab/cd/abcdef"},
		{"abcd", "ab/cd/abcd"},
		{"abc", "abc"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := s.objectKey(tc.in); got != tc.want {
			t.Errorf("objectKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Azure answers 400 InvalidBlockList when the block IDs of one blob decode to
// different lengths, and Azurite does not — so this is the only guard against
// a commit list that mixes our IDs with the SDK's 64-byte ones.
func TestAzureAppendState_BlockIDWidth(t *testing.T) {
	sdkID := base64.StdEncoding.EncodeToString(make([]byte, 64))
	legacyID := base64.StdEncoding.EncodeToString([]byte("aBcDeFgHiJk-000000")) // 18 bytes, pre-padding format

	tests := []struct {
		name  string
		state azureAppendState
		want  int
	}{
		{"fresh session pads to the sdk width", azureAppendState{}, azureBlockIDWidth},
		{"seeded sdk blocks keep 64 bytes", azureAppendState{BlockIDs: []string{sdkID}}, 64},
		{"session from an older release keeps its 18 bytes", azureAppendState{BlockIDs: []string{legacyID}}, 18},
		{"unparsable id falls back to the default", azureAppendState{BlockIDs: []string{"not base64!!"}}, azureBlockIDWidth},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.blockIDWidth(); got != tc.want {
				t.Fatalf("blockIDWidth() = %d, want %d", got, tc.want)
			}
			id, err := tc.state.nextBlockID()
			if err != nil {
				t.Fatalf("nextBlockID(): %v", err)
			}
			raw, derr := base64.StdEncoding.DecodeString(id)
			if derr != nil {
				t.Fatalf("nextBlockID() produced %q, not base64: %v", id, derr)
			}
			if len(raw) != tc.want {
				t.Fatalf("nextBlockID() decodes to %d bytes, want %d", len(raw), tc.want)
			}
		})
	}
}

// Every ID of one session must stay the same length however far the sequence
// counter runs, or the commit list breaks halfway through a large upload.
func TestAzureAppendState_NextBlockID_StableWidthAndUnique(t *testing.T) {
	st := &azureAppendState{Session: "aBcDeFgHiJk"}
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		id, err := st.nextBlockID()
		if err != nil {
			t.Fatalf("nextBlockID() at %d: %v", i, err)
		}
		raw, _ := base64.StdEncoding.DecodeString(id)
		if len(raw) != azureBlockIDWidth {
			t.Fatalf("id %d decodes to %d bytes, want %d", i, len(raw), azureBlockIDWidth)
		}
		if seen[id] {
			t.Fatalf("duplicate block id at %d: %q", i, id)
		}
		seen[id] = true
		st.BlockIDs = append(st.BlockIDs, id)
	}
}

// A width too small for the session id must fail loudly: truncating would let
// two different sequence numbers collide on one block.
func TestAzureAppendState_NextBlockID_TooNarrowErrors(t *testing.T) {
	narrow := base64.StdEncoding.EncodeToString([]byte("tiny"))
	st := &azureAppendState{Session: "aBcDeFgHiJk", BlockIDs: []string{narrow}}
	if _, err := st.nextBlockID(); err == nil {
		t.Fatal("nextBlockID() accepted a 4-byte width for an 18-byte id")
	}
}

// stubTokenCredential stands in for an Entra ID credential. The cache paths
// under test never reach the wire, so GetToken must never be called.
type stubTokenCredential struct{ t *testing.T }

func (s stubTokenCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	s.t.Fatal("GetToken called: the cached delegation key should have been reused")
	return azcore.AccessToken{}, nil
}

// A cached delegation key that still outlives the SAS must be reused — one
// Entra ID round trip per presigned URL is what this cache exists to avoid.
func TestAzureDelegationCredential_ReusesCachedKey(t *testing.T) {
	cached := &service.UserDelegationCredential{}
	s := &AzureBlobStore{
		serviceURL: "https://acct.blob.core.windows.net",
		tokenCred:  stubTokenCredential{t: t},
		udc:        cached,
		udcExpiry:  time.Now().UTC().Add(12 * time.Hour),
	}
	got, err := s.delegationCredential(context.Background(), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("delegationCredential(): %v", err)
	}
	if got != cached {
		t.Fatal("delegationCredential() minted a new key while the cached one was still valid")
	}
}

// A SAS that would outlive the 7-day ceiling of any delegation key has to
// fail before signing: the URL would be rejected at use time, long after the
// caller handed it out.
func TestAzureDelegationCredential_SASPastKeyCeiling(t *testing.T) {
	s := &AzureBlobStore{
		serviceURL: "https://acct.blob.core.windows.net",
		tokenCred:  stubTokenCredential{t: t},
	}
	if _, err := s.delegationCredential(context.Background(), time.Now().UTC().Add(8*24*time.Hour)); err == nil {
		t.Fatal("delegationCredential() accepted a SAS outliving the delegation key maximum")
	}
}
