// Package eepromupdater is the BMC-side analogue of rpi-eeprom-update: it
// finds and downloads the latest published pieeprom-*.bin from upstream
// (raspberrypi/rpi-eeprom on GitHub) so the BMC can stage it on the
// shared USB FAT for the host's rpi-eeprom-update to flash on next boot.
//
// We do NOT flash the EEPROM ourselves. The BMC runs out-of-band of the
// host OS and only manages the FAT volume both ends share over the USB
// gadget.
//
// Upstream layout (on the rpi-eeprom repo's default branch):
//
//	firmware-2712/<channel>/pieeprom-YYYY-MM-DD.bin   ← RPi 5 (BCM2712)
//	firmware-2711/<channel>/pieeprom-YYYY-MM-DD.bin   ← RPi 4 (BCM2711)
//
// where <channel> is "stable", "beta", or "critical". We list the channel
// directory via the GitHub Contents API and pick the lexicographically
// largest pieeprom-*.bin (date in filename, ISO 8601 — sorts correctly).
package eepromupdater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Platform identifies which SoC's EEPROM image directory we look in.
type Platform string

const (
	PlatformRPi5 Platform = "2712" // BCM2712 / RPi 5
	PlatformRPi4 Platform = "2711" // BCM2711 / RPi 4
)

// Channel matches rpi-eeprom's release channels. Default is stable: it's
// what rpi-eeprom-update uses out of the box and is the right choice for
// hands-off updates from a BMC.
type Channel string

const (
	ChannelStable   Channel = "stable"
	ChannelBeta     Channel = "beta"
	ChannelCritical Channel = "critical"
)

// Repo / branch the EEPROM binaries are published from.
const (
	upstreamOwner  = "raspberrypi"
	upstreamRepo   = "rpi-eeprom"
	upstreamBranch = "master"
)

// Image describes one published EEPROM binary on a channel.
type Image struct {
	Name     string    `json:"name"`     // e.g. "pieeprom-2024-12-04.bin"
	URL      string    `json:"url"`      // raw GitHub download URL
	Size     int64     `json:"size"`     // bytes
	Version  string    `json:"version"`  // "2024-12-04" parsed from Name
	Platform Platform  `json:"platform"` // 2712 or 2711
	Channel  Channel   `json:"channel"`  // stable/beta/critical
	FoundAt  time.Time `json:"foundAt"`  // when the lookup happened
}

// FindLatestOptions tunes FindLatest. Zero-value defaults are safe (RPi 5
// stable, default HTTP client + 15s timeout).
type FindLatestOptions struct {
	Platform   Platform
	Channel    Channel
	HTTPClient *http.Client
}

// FindLatest queries the GitHub Contents API for the most recent
// pieeprom-*.bin in (platform, channel) and returns its name + download
// URL + size. Returns the most recent by filename (ISO 8601 dates sort
// lexicographically).
//
// Network call: hits api.github.com without auth. Subject to GitHub's
// unauthenticated rate limits (60 req/hr/IP). Callers should cache the
// result rather than calling per UI refresh.
func FindLatest(ctx context.Context, opts FindLatestOptions) (*Image, error) {
	platform := opts.Platform
	if platform == "" {
		platform = PlatformRPi5
	}
	channel := opts.Channel
	if channel == "" {
		channel = ChannelStable
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	dir := fmt.Sprintf("firmware-%s/%s", platform, channel)
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
		upstreamOwner, upstreamRepo, dir, upstreamBranch)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("list %s: HTTP %d: %s", dir, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var entries []struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Size        int64  `json:"size"`
		DownloadURL string `json:"download_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode listing: %w", err)
	}

	// Filter to pieeprom-*.bin and sort descending by name. Filenames are
	// pieeprom-YYYY-MM-DD.bin so lexical descending == newest first.
	candidates := entries[:0]
	for _, e := range entries {
		if e.Type != "file" {
			continue
		}
		if !strings.HasPrefix(e.Name, "pieeprom-") || !strings.HasSuffix(e.Name, ".bin") {
			continue
		}
		if e.DownloadURL == "" {
			continue
		}
		candidates = append(candidates, e)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no pieeprom-*.bin entries found in %s", dir)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Name > candidates[j].Name
	})
	top := candidates[0]
	return &Image{
		Name:     top.Name,
		URL:      top.DownloadURL,
		Size:     top.Size,
		Version:  parseVersionFromName(top.Name),
		Platform: platform,
		Channel:  channel,
		FoundAt:  time.Now(),
	}, nil
}

// parseVersionFromName turns "pieeprom-2024-12-04.bin" into "2024-12-04".
// Returns the filename unchanged when it doesn't match the expected shape.
func parseVersionFromName(name string) string {
	s := strings.TrimPrefix(name, "pieeprom-")
	s = strings.TrimSuffix(s, ".bin")
	return s
}

// Download fetches img.URL and returns the bytes. Verifies the response
// size matches img.Size when img.Size > 0. Subject to ctx cancellation.
func Download(ctx context.Context, img *Image, client *http.Client) ([]byte, error) {
	if img == nil || img.URL == "" {
		return nil, errors.New("nil image or empty URL")
	}
	if client == nil {
		// EEPROM binaries are ~2 MB; allow generous time for slow links.
		client = &http.Client{Timeout: 2 * time.Minute}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", img.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: HTTP %d", img.Name, resp.StatusCode)
	}

	// Cap the read so a misbehaving server can't OOM us. Real images are
	// 512 KB or 2 MB; allow up to 4 MB headroom.
	const maxBytes = 4 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", img.Name, err)
	}
	if len(body) > maxBytes {
		return nil, fmt.Errorf("%s exceeded max size %d bytes", img.Name, maxBytes)
	}
	if img.Size > 0 && int64(len(body)) != img.Size {
		return nil, fmt.Errorf("%s size mismatch: got %d, expected %d", img.Name, len(body), img.Size)
	}
	return body, nil
}
