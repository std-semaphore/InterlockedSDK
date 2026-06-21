package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/Masterminds/semver/v3"
)

var signedFolders = []string{"Consists", "Diagrams", "Paths", "Static", "Templates", "TIPLOCs"}

type Manifest struct {
	Manifest ManifestMeta   `toml:"manifest"`
	Target   ManifestTarget `toml:"target"`
}

type ManifestMeta struct {
	ID      string `toml:"id"`
	Name    string `toml:"name"`
	Version string `toml:"version"`
	Author  string `toml:"author"`
}

type ManifestTarget struct {
	Game       string `toml:"game"`
	Sim        string `toml:"sim"`
	SimVersion string `toml:"sim_version"`
}

func loadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, "manifest.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no manifest.toml found in %s\nAre you inside a timetable directory?", dir)
		}
		return nil, fmt.Errorf("read manifest.toml: %w", err)
	}

	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest.toml: %w", err)
	}

	var missing []string
	if m.Manifest.ID == "" {
		missing = append(missing, "[manifest].id")
	}
	if m.Manifest.Version == "" {
		missing = append(missing, "[manifest].version")
	}
	if m.Manifest.Author == "" {
		missing = append(missing, "[manifest].author")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("manifest.toml is missing required fields: %s", strings.Join(missing, ", "))
	}

	if _, err := semver.NewVersion(m.Manifest.Version); err != nil {
		return nil, fmt.Errorf("manifest.toml: version %q is not valid semver (e.g. 1.0.0)", m.Manifest.Version)
	}

	return &m, nil
}

func isSignedPath(name string) bool {
	if name == "manifest.toml" {
		return true
	}
	parts := strings.SplitN(name, "/", 2)
	if len(parts) != 2 {
		return false
	}
	if strings.Contains(parts[1], "/") || filepath.Ext(parts[1]) != ".toml" {
		return false
	}
	for _, f := range signedFolders {
		if parts[0] == f {
			return true
		}
	}
	return false
}

func computeHash(files map[string][]byte) []byte {
	type entry struct{ path, hash string }
	var entries []entry
	for name, data := range files {
		if !isSignedPath(name) {
			continue
		}
		h := sha256.Sum256(data)
		entries = append(entries, entry{name, fmt.Sprintf("%x", h)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.path + ":" + e.hash + "\n")
	}
	final := sha256.Sum256([]byte(sb.String()))
	return final[:]
}

func compile(dir string, privKey ed25519.PrivateKey) ([]byte, map[string]int, error) {
	files := make(map[string][]byte)
	folderCounts := make(map[string]int)

	manifestData, err := os.ReadFile(filepath.Join(dir, "manifest.toml"))
	if err != nil {
		return nil, nil, fmt.Errorf("read manifest.toml: %w", err)
	}
	files["manifest.toml"] = manifestData

	for _, folder := range signedFolders {
		entries, err := os.ReadDir(filepath.Join(dir, folder))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read %s/: %w", folder, err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".toml" {
				continue
			}
			relPath := folder + "/" + e.Name()
			data, err := os.ReadFile(filepath.Join(dir, relPath))
			if err != nil {
				return nil, nil, fmt.Errorf("read %s: %w", relPath, err)
			}
			files[relPath] = data
			folderCounts[folder]++
		}
	}

	hash := computeHash(files)
	sig := ed25519.Sign(privKey, hash)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			return nil, nil, err
		}
		io.Copy(w, bytes.NewReader(data))
	}
	sigW, _ := zw.Create("signature.bin")
	sigW.Write(sig)
	zw.Close()

	return buf.Bytes(), folderCounts, nil
}

func verifySignature(zr *zip.Reader, pubKeyB64 string, hash []byte) error {
	pubKeyBytes, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key length %d", len(pubKeyBytes))
	}
	sig, err := readZipEntry(zr, "signature.bin")
	if err != nil {
		return fmt.Errorf("signature.bin missing: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pubKeyBytes), hash, sig) {
		return fmt.Errorf("signature does not match")
	}
	return nil
}

func computeHashFromZip(zr *zip.Reader) ([]byte, error) {
	files := make(map[string][]byte)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(f.Name)
		if !isSignedPath(name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		files[name] = data
	}
	return computeHash(files), nil
}

func openZip(data []byte) (*zip.Reader, error) {
	return zip.NewReader(bytes.NewReader(data), int64(len(data)))
}

func readZipEntry(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("file not found: %s", name)
}

func tomlUnmarshal(data []byte, v interface{}) error {
	return toml.Unmarshal(data, v)
}
