package glance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var spotifyWidgetTemplate = mustParseTemplate("spotify.html", "widget-base.html")

// Widget struct - main configuration and data
type spotifyWidget struct {
	widgetBase   `yaml:",inline"`
	ClientID     string         `yaml:"client-id"`
	ClientSecret string         `yaml:"client-secret"`
	Country      string         `yaml:"country"`
	Limit        int            `yaml:"limit"`
	ContentType  string         `yaml:"content-type"`
	Albums       []SpotifyAlbum `yaml:"-"`
}

// API Response structs (what Spotify sends us)
type SpotifyNewReleasesResponse struct {
	Albums struct {
		Href  string         `json:"href"`
		Limit int            `json:"limit"`
		Offset   int           `json:"offset"`
		Total    int           `json:"total"`
		Items []SpotifyAlbum `json:"items"`
	} `json:"albums"`
}

type SpotifyAlbum struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	AlbumType   string          `json:"album_type"`
	Artists     []SpotifyArtist `json:"artists"`
	Images      []SpotifyImage  `json:"images"`
	ReleaseDate string          `json:"release_date"`
	TotalTracks int             `json:"total_tracks"`
	ExternalUrls struct {
		Spotify string `json:"spotify"`
	} `json:"external_urls"`
}

type SpotifyArtist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type SpotifyImage struct {
	Height int    `json:"height"`
	Width  int    `json:"width"`
	URL    string `json:"url"`
}

type SpotifyTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// Required widget interface methods
func (w *spotifyWidget) initialize() error {
	// Set default title and cache duration (1 hour like other API widgets)
	w.withTitle("Spotify").withCacheDuration(time.Hour)
	
	// Validate required fields - these MUST be provided by user
	if w.ClientID == "" {
		return fmt.Errorf("client-id is required")
	}
	if w.ClientSecret == "" {
		return fmt.Errorf("client-secret is required")
	}
	
	// Set defaults for optional fields
	if w.Country == "" {
		w.Country = "US"  // Default to US market
	}
	if w.Limit <= 0 {
		w.Limit = 10      // Default to 10 albums
	}
	if w.ContentType == "" {
		w.ContentType = "new-releases"  // Default content type
	}
	
	return nil
}

func (w *spotifyWidget) update(ctx context.Context) {
	// Step 1: Get access token
	token, err := w.getAccessToken(ctx)
	if err != nil {
		w.canContinueUpdateAfterHandlingErr(err)
		return
	}
	
	// Step 2: Fetch album data
	albums, err := w.fetchNewReleases(ctx, token)
	if err != nil {
		w.canContinueUpdateAfterHandlingErr(err)
		return
	}
	
	// Step 3: Store the data for rendering
	w.Albums = albums
	
	// Step 4: Mark success - this sets ContentAvailable = true
	w.canContinueUpdateAfterHandlingErr(nil)
}

func (w *spotifyWidget) Render() template.HTML {
	return w.renderTemplate(w, spotifyWidgetTemplate)
}

// Helper methods for API calls
func (w *spotifyWidget) getAccessToken(ctx context.Context) (string, error) {
	// Encode credentials (like our base64 in curl)
	credentials := base64.StdEncoding.EncodeToString(
		[]byte(w.ClientID + ":" + w.ClientSecret),
	)
	
	// Prepare request body
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	
	// Create request 
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		"https://accounts.spotify.com/api/token",
		strings.NewReader(data.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}
	
	// Set headers (like our curl -H flags)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+credentials)
	
	// Make the request 
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request returned status %d", resp.StatusCode)
	}

	// Parse response
	var tokenResp SpotifyTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	return tokenResp.AccessToken, nil
}

func (w *spotifyWidget) fetchNewReleases(ctx context.Context, token string) ([]SpotifyAlbum, error) {
	// Build URL with query parameters 
	apiURL := fmt.Sprintf(
		"https://api.spotify.com/v1/browse/new-releases?limit=%d&country=%s",
		w.Limit,
		w.Country,
	)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create API request: %w", err)
	}

	// Set authorization header (like our curl -H)
	req.Header.Set("Authorization", "Bearer "+token)

	// Make request
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	// Parse response
	var apiResp SpotifyNewReleasesResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode API response: %w", err)
	}
	
	return apiResp.Albums.Items, nil
}

// Helper method to get best image size
func (album SpotifyAlbum) GetImageURL(preferredSize int) string {
	if len(album.Images) == 0 {
		return "" // No images available
	}
	
	// Find the image closest to our preferred size
	bestImage := album.Images[0] // Start with first image
	bestDiff := abs(bestImage.Width - preferredSize)
	
	// Check all images and find the one closest to preferred size
	for _, img := range album.Images {
		diff := abs(img.Width - preferredSize)
		if diff < bestDiff {
			bestImage = img
			bestDiff = diff
		}
	}
	
	return bestImage.URL
}

// Helper method to get main artist name
func (album SpotifyAlbum) GetMainArtist() string {
	if len(album.Artists) == 0 {
		return "Unknown Artist"
	}
	// Return the first artist (usually the main one)
	return album.Artists[0].Name
}

// Helper function for absolute value (Go doesn't have built-in abs for int)
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}