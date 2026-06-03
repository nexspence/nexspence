package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func init() { gin.SetMode(gin.TestMode) }

func newGroupHandler(stores ...*domain.BlobStore) *handlers.BlobStoreHandler {
	return handlers.NewBlobStoreHandler(testutil.NewBlobStoreRepo(stores...))
}

func postGroupCreate(h *handlers.BlobStoreHandler, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/blobstores/:type", h.Create)
	req := httptest.NewRequest(http.MethodPost, "/blobstores/group", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func deleteStore(h *handlers.BlobStoreHandler, name string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.DELETE("/blobstores/:name", h.Delete)
	req := httptest.NewRequest(http.MethodDelete, "/blobstores/"+name, nil)
	r.ServeHTTP(w, req)
	return w
}

func TestBlobStoreHandler_Create_Group_Valid(t *testing.T) {
	memberA := &domain.BlobStore{ID: "aaa", Name: "store-a", Type: "local"}
	memberB := &domain.BlobStore{ID: "bbb", Name: "store-b", Type: "local"}
	h := newGroupHandler(memberA, memberB)

	w := postGroupCreate(h, map[string]any{
		"name": "my-group",
		"config": map[string]any{
			"fill_policy": "round_robin",
			"member_ids":  []string{"aaa", "bbb"},
		},
	})
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestBlobStoreHandler_Create_Group_InvalidPolicy(t *testing.T) {
	memberA := &domain.BlobStore{ID: "aaa", Name: "store-a", Type: "local"}
	h := newGroupHandler(memberA)

	w := postGroupCreate(h, map[string]any{
		"name": "bad-group",
		"config": map[string]any{
			"fill_policy": "teleport",
			"member_ids":  []string{"aaa"},
		},
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBlobStoreHandler_Create_Group_NoMembers(t *testing.T) {
	h := newGroupHandler()
	w := postGroupCreate(h, map[string]any{
		"name": "empty-group",
		"config": map[string]any{
			"fill_policy": "round_robin",
			"member_ids":  []string{},
		},
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBlobStoreHandler_Create_Group_UnknownMember(t *testing.T) {
	h := newGroupHandler()
	w := postGroupCreate(h, map[string]any{
		"name": "ghost-group",
		"config": map[string]any{
			"fill_policy": "round_robin",
			"member_ids":  []string{"does-not-exist"},
		},
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBlobStoreHandler_Create_Group_NestedGroup_Rejected(t *testing.T) {
	inner := &domain.BlobStore{ID: "inner-g", Name: "inner-group", Type: "group",
		Config: map[string]any{"fill_policy": "round_robin", "member_ids": []string{}}}
	h := newGroupHandler(inner)

	w := postGroupCreate(h, map[string]any{
		"name": "outer-group",
		"config": map[string]any{
			"fill_policy": "round_robin",
			"member_ids":  []string{"inner-g"},
		},
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBlobStoreHandler_Delete_MemberOfGroup_Rejected(t *testing.T) {
	member := &domain.BlobStore{ID: "mem-1", Name: "store-a", Type: "local"}
	group := &domain.BlobStore{
		ID: "grp-1", Name: "my-group", Type: "group",
		Config: map[string]any{
			"fill_policy": "round_robin",
			"member_ids":  []interface{}{"mem-1"},
		},
	}
	h := newGroupHandler(member, group)

	w := deleteStore(h, "store-a")
	require.Equal(t, http.StatusConflict, w.Code)
}
