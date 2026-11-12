package api

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func TestProxyStreamFailure_AbortsIncompleteResponse(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		name := "HTTP1"
		if http2 {
			name = "HTTP2"
		}
		t.Run(name, func(t *testing.T) {
			clientRead := make(chan struct{})
			var once sync.Once
			// This server acts as a trusted outbound proxy. Its chunked body
			// fails only after the downstream client has received a prefix.
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, strings.Repeat("a", 32<<10))
				w.(http.Flusher).Flush()
				<-clientRead
				conn, _, err := http.NewResponseController(w).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}))
			defer upstream.Close()
			repo := &domain.Repository{ID: "stream-abort", Name: "stream-abort-" + name, Type: domain.TypeProxy,
				ProxyConfig: map[string]any{"remote_url": "http://upstream.example", "http_proxy": upstream.URL}}
			d := formats.Deps{Assets: testutil.NewAssetRepo(), Components: testutil.NewComponentRepo(),
				Blobs: testutil.NewBlobStoreRepo(), BlobStore: testutil.NewBlobStore()}
			router := gin.New()
			router.Use(recoveryMiddleware())
			router.GET("/artifact", func(c *gin.Context) {
				if err := repoproxy.ServeGET(c, d, repo, "/artifact", "", base.Coords{}, "application/octet-stream", 0); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				}
			})
			server := httptest.NewUnstartedServer(router)
			server.EnableHTTP2 = http2
			if http2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			defer once.Do(func() { close(clientRead) })
			client := server.Client()
			client.Timeout = 5 * time.Second
			resp, err := client.Get(server.URL + "/artifact")
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			if http2 {
				require.Equal(t, 2, resp.ProtoMajor)
			}
			require.EqualValues(t, -1, resp.ContentLength)
			_, err = io.ReadFull(resp.Body, make([]byte, 1))
			require.NoError(t, err)
			once.Do(func() { close(clientRead) })
			_, err = io.ReadAll(resp.Body)
			require.Error(t, err, "an incomplete artifact must not have a successful stream terminator")
			var networkErr net.Error
			require.False(t, errors.As(err, &networkErr) && networkErr.Timeout(), "stream must abort rather than time out")
			asset, _ := d.Assets.GetByPath(context.Background(), repo.Name, "/artifact")
			require.Nil(t, asset, "failed fill must not be registered")
		})
	}
}

func TestRecoveryMiddleware_RecoversOrdinaryPanic(t *testing.T) {
	router := gin.New()
	router.Use(recoveryMiddleware())
	router.GET("/panic", func(*gin.Context) { panic("unexpected failure") })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/panic", nil))
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

type unavailableProxyStore struct{ *testutil.BlobStore }

func (unavailableProxyStore) Put(context.Context, string, io.Reader, int64) error {
	return errors.New("storage unavailable")
}

func TestProxyStreamFailure_BeforeBodyPreservesErrorResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "8192")
		_, _ = io.WriteString(w, strings.Repeat("a", 8192))
	}))
	defer upstream.Close()
	repo := &domain.Repository{ID: "before-body", Name: "before-body", Type: domain.TypeProxy,
		ProxyConfig: map[string]any{"remote_url": "http://upstream.example", "http_proxy": upstream.URL}}
	d := formats.Deps{Assets: testutil.NewAssetRepo(), Components: testutil.NewComponentRepo(),
		Blobs: testutil.NewBlobStoreRepo(), BlobStore: unavailableProxyStore{testutil.NewBlobStore()}}
	router := gin.New()
	router.Use(recoveryMiddleware())
	router.GET("/artifact", func(c *gin.Context) {
		if err := repoproxy.ServeGET(c, d, repo, "/artifact", "", base.Coords{}, "application/octet-stream", 0); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
	})
	server := httptest.NewServer(router)
	defer server.Close()
	resp, err := server.Client().Get(server.URL + "/artifact")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "error response must not retain artifact Content-Length")
	require.Contains(t, string(body), "storage unavailable")
}
