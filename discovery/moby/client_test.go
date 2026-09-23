// Copyright 2026 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package moby

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-kit/log"
	"github.com/moby/moby/client"
	"github.com/prometheus/common/config"
	"github.com/stretchr/testify/require"
)

func TestDiscoveryClientAPINegotiation(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	t.Setenv("DOCKER_API_VERSION", "1.39")

	for _, kind := range []string{"docker", "dockerswarm"} {
		for _, apiVersion := range []string{"1.40", client.MaxAPIVersion} {
			t.Run(kind+"/"+apiVersion, func(t *testing.T) {
				listCalls := 0
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, userAgent, r.UserAgent())
					username, password, ok := r.BasicAuth()
					require.True(t, ok)
					require.Equal(t, "test-user", username)
					require.Equal(t, "test-password", password)
					switch r.URL.Path {
					case "/_ping":
						w.Header().Set("API-Version", apiVersion)
					case fmt.Sprintf("/v%s/containers/json", apiVersion):
						listCalls++
						w.Header().Set("Content-Type", "application/json")
						_, err := w.Write([]byte(`[]`))
						require.NoError(t, err)
					default:
						t.Errorf("unexpected request: %s", r.URL)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()

				httpConfig := config.DefaultHTTPClientConfig
				httpConfig.TLSConfig.InsecureSkipVerify = true
				httpConfig.BasicAuth = &config.BasicAuth{
					Username: "test-user",
					Password: "test-password",
				}
				var dockerClient *client.Client
				if kind == "docker" {
					cfg := DefaultDockerSDConfig
					cfg.Host = server.URL
					cfg.HTTPClientConfig = httpConfig
					d, err := NewDockerDiscovery(&cfg, log.NewNopLogger())
					require.NoError(t, err)
					dockerClient = d.client
				} else {
					cfg := DefaultDockerSwarmSDConfig
					cfg.Host = server.URL
					cfg.Role = "nodes"
					cfg.HTTPClientConfig = httpConfig
					d, err := NewDiscovery(&cfg, log.NewNopLogger())
					require.NoError(t, err)
					dockerClient = d.client
				}
				defer dockerClient.Close()

				result, err := dockerClient.ContainerList(context.Background(), client.ContainerListOptions{})
				require.NoError(t, err)
				require.Empty(t, result.Items)
				require.Equal(t, apiVersion, dockerClient.ClientVersion())
				require.Equal(t, 1, listCalls)
			})
		}
	}
}

func TestDiscoveryClientUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Docker Unix socket transport is tested on Unix")
	}
	for _, kind := range []string{"docker", "dockerswarm"} {
		t.Run(kind, func(t *testing.T) {
			socketPath := filepath.Join(t.TempDir(), "docker.sock")
			listener, err := net.Listen("unix", socketPath)
			require.NoError(t, err)

			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/_ping":
					w.Header().Set("API-Version", "1.40")
				case "/v1.40/containers/json":
					w.Header().Set("Content-Type", "application/json")
					_, err := w.Write([]byte(`[]`))
					require.NoError(t, err)
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			require.NoError(t, server.Listener.Close())
			server.Listener = listener
			server.Start()
			defer server.Close()

			var dockerClient *client.Client
			if kind == "docker" {
				cfg := DefaultDockerSDConfig
				cfg.Host = "unix://" + socketPath
				d, err := NewDockerDiscovery(&cfg, log.NewNopLogger())
				require.NoError(t, err)
				dockerClient = d.client
			} else {
				cfg := DefaultDockerSwarmSDConfig
				cfg.Host = "unix://" + socketPath
				cfg.Role = "nodes"
				d, err := NewDiscovery(&cfg, log.NewNopLogger())
				require.NoError(t, err)
				dockerClient = d.client
			}
			defer dockerClient.Close()
			result, err := dockerClient.ContainerList(context.Background(), client.ContainerListOptions{})
			require.NoError(t, err)
			require.Empty(t, result.Items)
		})
	}
}
