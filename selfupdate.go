package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	ghcrImage        = "ghcr.io/jrsmile/blizbase"
	ghcrTag          = "latest"
	ghcrImageRef     = ghcrImage + ":" + ghcrTag
	ghcrRegistryURL  = "https://ghcr.io"
	dockerSocketPath = "/var/run/docker.sock"
)

// dockerHTTPClient creates an HTTP client that talks to the Docker daemon via Unix socket.
func dockerHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", dockerSocketPath)
			},
		},
		Timeout: 120 * time.Second,
	}
}

// getGHCRToken fetches an anonymous bearer token for pulling public image manifests from GHCR.
func getGHCRToken(ctx context.Context, repo string) (string, error) {
	tokenURL := fmt.Sprintf("https://ghcr.io/token?scope=repository:%s:pull&service=ghcr.io", repo)
	req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request GHCR token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GHCR token request failed (%d): %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}
	return tokenResp.Token, nil
}

// getRemoteDigest queries the GHCR registry v2 API for the current digest of the :latest tag.
func getRemoteDigest(ctx context.Context) (string, error) {
	repo := strings.TrimPrefix(ghcrImage, "ghcr.io/")

	token, err := getGHCRToken(ctx, repo)
	if err != nil {
		return "", err
	}

	manifestURL := fmt.Sprintf("%s/v2/%s/manifests/%s", ghcrRegistryURL, repo, ghcrTag)
	req, err := http.NewRequestWithContext(ctx, "HEAD", manifestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.oci.image.index.v1+json",
	}, ", "))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch remote manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("manifest request failed (%d): %s", resp.StatusCode, body)
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("no Docker-Content-Digest header in registry response")
	}
	return digest, nil
}

// getLocalDigest inspects the locally pulled image via the Docker Engine API
// and returns its repo digest (e.g. sha256:abc...).
func getLocalDigest(ctx context.Context) (string, error) {
	client := dockerHTTPClient()

	req, err := http.NewRequestWithContext(ctx, "GET", "http://localhost/images/"+ghcrImageRef+"/json", nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to inspect local image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil // image not present locally yet
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("image inspect failed (%d): %s", resp.StatusCode, body)
	}

	var imageInfo struct {
		RepoDigests []string `json:"RepoDigests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&imageInfo); err != nil {
		return "", fmt.Errorf("failed to decode image info: %w", err)
	}

	for _, d := range imageInfo.RepoDigests {
		if strings.HasPrefix(d, ghcrImage+"@") {
			return strings.TrimPrefix(d, ghcrImage+"@"), nil
		}
	}
	return "", nil
}

// pullImage tells the Docker daemon to pull the latest image from GHCR.
func pullImage(ctx context.Context) error {
	client := dockerHTTPClient()

	url := fmt.Sprintf("http://localhost/images/create?fromImage=%s&tag=%s", ghcrImage, ghcrTag)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pull failed (%d): %s", resp.StatusCode, body)
	}

	// Docker streams pull progress as newline-delimited JSON — read to completion.
	decoder := json.NewDecoder(resp.Body)
	for {
		var event map[string]interface{}
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("error reading pull stream: %w", err)
		}
		if errMsg, ok := event["error"]; ok {
			return fmt.Errorf("pull error: %v", errMsg)
		}
	}

	return nil
}

// getContainerID returns the ID of the running container using the given image.
func getContainerID(ctx context.Context) (string, error) {
	client := dockerHTTPClient()

	req, err := http.NewRequestWithContext(ctx, "GET",
		"http://localhost/containers/json?filters="+`{"ancestor":["`+ghcrImageRef+`"]}`, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("list containers failed (%d): %s", resp.StatusCode, body)
	}

	var containers []struct {
		Id string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return "", fmt.Errorf("failed to decode container list: %w", err)
	}

	if len(containers) == 0 {
		return "", nil
	}
	return containers[0].Id, nil
}

// restartContainer sends a restart request to the Docker daemon for the given container.
func restartContainer(ctx context.Context, containerID string) error {
	client := dockerHTTPClient()

	url := fmt.Sprintf("http://localhost/containers/%s/restart?t=10", containerID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to restart container: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("restart failed (%d): %s", resp.StatusCode, body)
	}
	return nil
}

// watchForUpdates checks the GHCR registry for a newer image digest,
// pulls it if changed, and optionally restarts the running container.
// Designed to be called periodically via cron.
func watchForUpdates() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	log.Println("[selfupdate] Checking for image updates...")

	remoteDigest, err := getRemoteDigest(ctx)
	if err != nil {
		log.Printf("[selfupdate] Error checking remote digest: %v", err)
		return
	}
	log.Printf("[selfupdate] Remote digest: %s", remoteDigest)

	localDigest, err := getLocalDigest(ctx)
	if err != nil {
		log.Printf("[selfupdate] Error checking local digest: %v", err)
		return
	}
	log.Printf("[selfupdate] Local  digest: %s", localDigest)

	if localDigest == remoteDigest {
		log.Println("[selfupdate] Image is up to date.")
		return
	}

	log.Println("[selfupdate] New image version detected, pulling...")
	if err := pullImage(ctx); err != nil {
		log.Printf("[selfupdate] Error pulling image: %v", err)
		return
	}
	log.Println("[selfupdate] Successfully pulled new image.")

	// Find and restart our own container so it picks up the new image.
	containerID, err := getContainerID(ctx)
	if err != nil {
		log.Printf("[selfupdate] Error finding container: %v", err)
		return
	}
	if containerID == "" {
		log.Println("[selfupdate] No running container found for this image. Pull complete; manual restart needed.")
		os.Exit(1)
		return
	}

	log.Printf("[selfupdate] Restarting container %s...", containerID[:12])
	if err := restartContainer(ctx, containerID); err != nil {
		log.Printf("[selfupdate] Error restarting container: %v", err)
		os.Exit(1)
		return
	}
}
