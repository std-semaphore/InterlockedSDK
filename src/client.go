package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type apiClient struct {
	http *http.Client
}

func newClient() *apiClient {
	return &apiClient{http: &http.Client{}}
}

func (c *apiClient) fetchNonce(authorID string) (string, error) {
	resp, err := c.http.Get(fmt.Sprintf("%s/challenge/%s", registryURL, authorID))
	if err != nil {
		return "", fmt.Errorf("fetch challenge: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("challenge failed (%d): %s", resp.StatusCode, string(body))
	}
	var result struct {
		Nonce string `json:"nonce"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Nonce, nil
}

func (c *apiClient) signedRequest(method, url string, body io.Reader, authorID string, privKey ed25519.PrivateKey) (*http.Request, error) {
	nonce, err := c.fetchNonce(authorID)
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(privKey, []byte(nonce))
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(sig))
	return req, nil
}

type Author struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	PublicKey   string `json:"public_key"`
}

func (c *apiClient) GetAuthor(id string) (*Author, error) {
	resp, err := c.http.Get(fmt.Sprintf("%s/authors/%s", registryURL, id))
	if err != nil {
		return nil, fmt.Errorf("get author: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("author %q not found", id)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get author failed (%d): %s", resp.StatusCode, string(body))
	}
	var a Author
	json.NewDecoder(resp.Body).Decode(&a)
	return &a, nil
}

func (c *apiClient) CreateAuthor(a Author) error {
	data, _ := json.Marshal(a)
	resp, err := c.http.Post(registryURL+"/authors", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *apiClient) DeleteAuthor(id string, privKey ed25519.PrivateKey) error {
	req, err := c.signedRequest("DELETE", fmt.Sprintf("%s/authors/%s", registryURL, id), nil, id, privKey)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("deregister: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("deregister failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

type VersionEntry struct {
	Version string `json:"version"`
	Yanked  bool   `json:"yanked"`
}

type Timetable struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Publisher string         `json:"publisher"`
	Versions  []VersionEntry `json:"versions"`
}

func (c *apiClient) UploadTimetable(archiveData []byte, authorID string, privKey ed25519.PrivateKey) error {
	req, err := c.signedRequest("POST", registryURL+"/timetables", bytes.NewReader(archiveData), authorID, privKey)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("X-Author-ID", authorID)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *apiClient) GetTimetable(id string) (*Timetable, error) {
	resp, err := c.http.Get(fmt.Sprintf("%s/timetables/%s", registryURL, id))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("timetable %q not found on registry", id)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("info failed (%d): %s", resp.StatusCode, string(body))
	}
	var tt Timetable
	json.NewDecoder(resp.Body).Decode(&tt)
	return &tt, nil
}

type ListResult struct {
	Page       int         `json:"page"`
	Limit      int         `json:"limit"`
	Timetables []Timetable `json:"timetables"`
}

func (c *apiClient) ListTimetables(page, limit int) (*ListResult, error) {
	url := fmt.Sprintf("%s/timetables?page=%d&limit=%d", registryURL, page, limit)
	resp, err := c.http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list failed (%d): %s", resp.StatusCode, string(body))
	}
	var result ListResult
	json.NewDecoder(resp.Body).Decode(&result)
	return &result, nil
}

func (c *apiClient) DownloadTimetable(id, version string) ([]byte, error) {
	resp, err := c.http.Get(fmt.Sprintf("%s/timetables/%s/%s", registryURL, id, version))
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return io.ReadAll(resp.Body)
	case http.StatusNotFound:
		return nil, fmt.Errorf("timetable %s@%s not found", id, version)
	case http.StatusGone:
		return nil, fmt.Errorf("version %s has been yanked", version)
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed (%d): %s", resp.StatusCode, string(body))
	}
}

func (c *apiClient) YankVersion(id, version, authorID string, privKey ed25519.PrivateKey) error {
	url := fmt.Sprintf("%s/timetables/%s/%s/yank", registryURL, id, version)
	req, err := c.signedRequest("POST", url, nil, authorID, privKey)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("yank: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("yank failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}
