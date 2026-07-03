package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/Masterminds/semver/v3"
)

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

// computeHash returns the sha256 hash of the raw bytes that get signed —
// the serialized timetable.json itself, rather than a manifest over many
// individually-signed TOML files.
func computeHash(jsonData []byte) []byte {
	h := sha256.Sum256(jsonData)
	return h[:]
}

// compile converts the timetable directory into a single JSON document
// (via buildOutput, see convert.go), signs a hash of that JSON, and
// packages both as a gzip-compressed tar archive:
//
//	timetable.json
//	signature.bin
//
// The returned counts map reports sizes of a few top-level sections of
// the JSON document, for progress-printing purposes only.
func compile(dir string, privKey ed25519.PrivateKey) ([]byte, map[string]int, error) {
	doc, err := buildOutput(dir, "")
	if err != nil {
		return nil, nil, err
	}

	jsonData, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal timetable.json: %w", err)
	}

	hash := computeHash(jsonData)
	sig := ed25519.Sign(privKey, hash)

	counts := map[string]int{
		"tiplocs":  len(doc.Tiplocs),
		"paths":    len(doc.Paths),
		"consists": len(doc.Consists),
		"stations": len(doc.Stations),
		"diagrams": len(doc.Diagrams),
	}

	archive, err := buildTarGz(jsonData, sig)
	if err != nil {
		return nil, nil, err
	}

	return archive, counts, nil
}

func buildTarGz(jsonData, sig []byte) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	files := []struct {
		name string
		data []byte
	}{
		{"timetable.json", jsonData},
		{"signature.bin", sig},
	}

	for _, f := range files {
		hdr := &tar.Header{
			Name: f.name,
			Mode: 0o644,
			Size: int64(len(f.data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("write tar header for %s: %w", f.name, err)
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, fmt.Errorf("write tar data for %s: %w", f.name, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("close gzip writer: %w", err)
	}
	return buf.Bytes(), nil
}

// TarGzReader holds the decoded contents of a timetable .tar.gz archive,
// keyed by entry name (e.g. "timetable.json", "signature.bin").
type TarGzReader struct {
	Files map[string][]byte
}

func openTarGz(data []byte) (*TarGzReader, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a gzip stream: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		files[filepath.ToSlash(hdr.Name)] = buf
	}
	return &TarGzReader{Files: files}, nil
}

func readTarEntry(tgz *TarGzReader, name string) ([]byte, error) {
	data, ok := tgz.Files[name]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", name)
	}
	return data, nil
}

// computeHashFromTarGz extracts timetable.json from the archive and
// returns its sha256 hash — the same hash that was signed at compile time.
func computeHashFromTarGz(tgz *TarGzReader) ([]byte, error) {
	jsonData, err := readTarEntry(tgz, "timetable.json")
	if err != nil {
		return nil, err
	}
	return computeHash(jsonData), nil
}

func verifySignature(tgz *TarGzReader, pubKeyB64 string, hash []byte) error {
	pubKeyBytes, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key length %d", len(pubKeyBytes))
	}
	sig, err := readTarEntry(tgz, "signature.bin")
	if err != nil {
		return fmt.Errorf("signature.bin missing: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pubKeyBytes), hash, sig) {
		return fmt.Errorf("signature does not match")
	}
	return nil
}

func tomlUnmarshal(data []byte, v interface{}) error {
	return toml.Unmarshal(data, v)
}
