package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"github.com/spf13/cobra"
)

func registerCmd() *cobra.Command {
	var displayName string

	cmd := &cobra.Command{
		Use:   "register <id>",
		Short: "Create your author account on the registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			authorID := args[0]

			privB64, pubB64, err := generateKeypair()
			if err != nil {
				return fmt.Errorf("generate keypair: %w", err)
			}

			fmt.Printf("Registering %q ...\n", authorID)

			client := newClient()
			if err := client.CreateAuthor(Author{
				ID:          authorID,
				DisplayName: displayName,
				PublicKey:   pubB64,
			}); err != nil {
				return err
			}

			creds := &Credentials{AuthorID: authorID, PrivateKey: privB64}
			if err := saveCredentials(creds); err != nil {
				return fmt.Errorf("save credentials: %w", err)
			}

			fmt.Printf("✓ Registered as %q\n", authorID)
			fmt.Println("  Credentials saved to ~/.intsdk/credentials.toml")
			return nil
		},
	}

	cmd.Flags().StringVar(&displayName, "display-name", "", "Display name (optional)")
	return cmd
}

func deregisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deregister",
		Short: "Delete your author account and all your timetables",
		RunE: func(cmd *cobra.Command, args []string) error {
			creds, err := loadCredentials()
			if err != nil {
				return err
			}

			fmt.Printf("This will permanently delete author %q and ALL their timetables.\n", creds.AuthorID)
			if !confirm("Are you sure?") {
				fmt.Println("Aborted.")
				return nil
			}

			privKey, err := privateKeyFromCreds(creds)
			if err != nil {
				return err
			}

			client := newClient()
			if err := client.DeleteAuthor(creds.AuthorID, privKey); err != nil {
				return err
			}

			path, _ := credentialsPath()
			os.Remove(path)

			fmt.Printf("✓ Deregistered %q\n", creds.AuthorID)
			return nil
		},
	}
}

func compileCmd() *cobra.Command {
	var dir string
	var out string

	cmd := &cobra.Command{
		Use:   "compile [path]",
		Short: "Build a signed timetable archive without uploading",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				dir = args[0]
			}
			if dir == "" {
				dir, _ = os.Getwd()
			}

			creds, err := loadCredentials()
			if err != nil {
				return err
			}

			manifest, err := loadManifest(dir)
			if err != nil {
				return err
			}

			if manifest.Manifest.Author != creds.AuthorID {
				return fmt.Errorf(
					"manifest author %q doesn't match your registered ID %q",
					manifest.Manifest.Author, creds.AuthorID,
				)
			}

			privKey, err := privateKeyFromCreds(creds)
			if err != nil {
				return err
			}

			fmt.Printf("Compiling %s@%s ...\n", manifest.Manifest.ID, manifest.Manifest.Version)

			archiveData, counts, err := compile(dir, privKey)
			if err != nil {
				return err
			}

			fmt.Println("  timetable.json")
			for _, section := range []string{"tiplocs", "paths", "consists", "stations", "diagrams"} {
				if n := counts[section]; n > 0 {
					fmt.Printf("    %-10s%d\n", section+":", n)
				}
			}
			fmt.Printf("  signature.bin\n")
			fmt.Printf("\n  %.1f KB signed and packaged\n", float64(len(archiveData))/1024)

			if out == "" {
				out = fmt.Sprintf("%s-%s.tar.gz", manifest.Manifest.ID, manifest.Manifest.Version)
			}
			if err := os.WriteFile(out, archiveData, 0644); err != nil {
				return fmt.Errorf("write %s: %w", out, err)
			}

			fmt.Printf("\n✓ Written to %s\n", out)
			return nil
		},
	}

	cmd.Flags().StringVarP(&dir, "dir", "d", "", "Timetable directory (default: current directory)")
	cmd.Flags().StringVarP(&out, "out", "o", "", "Output archive path (default: <id>-<version>.tar.gz)")
	return cmd
}

func publishCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "publish [path]",
		Short: "Build and upload a timetable to the registry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				dir = args[0]
			}
			if dir == "" {
				dir, _ = os.Getwd()
			}

			creds, err := loadCredentials()
			if err != nil {
				return err
			}

			manifest, err := loadManifest(dir)
			if err != nil {
				return err
			}

			if manifest.Manifest.Author != creds.AuthorID {
				return fmt.Errorf(
					"manifest author %q doesn't match your registered ID %q",
					manifest.Manifest.Author, creds.AuthorID,
				)
			}

			privKey, err := privateKeyFromCreds(creds)
			if err != nil {
				return err
			}

			fmt.Printf("Publishing %s@%s ...\n", manifest.Manifest.ID, manifest.Manifest.Version)

			archiveData, _, err := compile(dir, privKey)
			if err != nil {
				return err
			}

			client := newClient()
			if err := client.UploadTimetable(archiveData, creds.AuthorID, privKey); err != nil {
				return err
			}

			fmt.Printf("✓ Published %s@%s (%.1f KB)\n",
				manifest.Manifest.ID, manifest.Manifest.Version, float64(len(archiveData))/1024)
			return nil
		},
	}

	cmd.Flags().StringVarP(&dir, "dir", "d", "", "Timetable directory (default: current directory)")
	return cmd
}

func yankCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "yank [version]",
		Short: "Yank a published version",
		Long: `Marks a version as yanked. It remains on the registry but can
no longer be downloaded. Uses the version in manifest.toml if not specified.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			creds, err := loadCredentials()
			if err != nil {
				return err
			}

			if dir == "" {
				dir, _ = os.Getwd()
			}

			manifest, err := loadManifest(dir)
			if err != nil {
				return err
			}

			version := manifest.Manifest.Version
			if len(args) > 0 {
				version = args[0]
			}

			privKey, err := privateKeyFromCreds(creds)
			if err != nil {
				return err
			}

			client := newClient()
			if err := client.YankVersion(manifest.Manifest.ID, version, creds.AuthorID, privKey); err != nil {
				return err
			}

			fmt.Printf("✓ Yanked %s@%s\n", manifest.Manifest.ID, version)
			return nil
		},
	}

	cmd.Flags().StringVarP(&dir, "dir", "d", "", "Timetable directory (default: current directory)")
	return cmd
}

func infoCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "info [id]",
		Short: "Show registry info for a timetable",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var id string

			if len(args) > 0 {
				id = args[0]
			} else {
				if dir == "" {
					dir, _ = os.Getwd()
				}
				m, err := loadManifest(dir)
				if err != nil {
					return err
				}
				id = m.Manifest.ID
			}

			client := newClient()
			tt, err := client.GetTimetable(id)
			if err != nil {
				return err
			}

			fmt.Printf("%-12s %s\n", "ID:", tt.ID)
			fmt.Printf("%-12s %s\n", "Name:", tt.Name)
			fmt.Printf("%-12s %s\n", "Publisher:", tt.Publisher)
			fmt.Printf("%-12s\n", "Versions:")

			if len(tt.Versions) == 0 {
				fmt.Println("  (none)")
			}
			for _, v := range tt.Versions {
				tag := ""
				if v.Yanked {
					tag = "  [yanked]"
				}
				fmt.Printf("  %s%s\n", v.Version, tag)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&dir, "dir", "d", "", "Timetable directory (default: current directory)")
	return cmd
}

func whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show your registered author profile and timetables",
		RunE: func(cmd *cobra.Command, args []string) error {
			creds, err := loadCredentials()
			if err != nil {
				return err
			}

			client := newClient()
			author, err := client.GetAuthor(creds.AuthorID)
			if err != nil {
				return err
			}

			name := author.DisplayName
			if name == "" {
				name = "(no display name)"
			}
			fmt.Printf("%-12s %s\n", "ID:", author.ID)
			fmt.Printf("%-12s %s\n", "Name:", name)
			fmt.Printf("%-12s %s\n", "Public key:", author.PublicKey)

			result, err := client.ListTimetables(1, 100)
			if err != nil {
				return err
			}

			var mine []Timetable
			for _, tt := range result.Timetables {
				if tt.Publisher == creds.AuthorID {
					mine = append(mine, tt)
				}
			}

			fmt.Printf("\nTimetables (%d):\n", len(mine))
			if len(mine) == 0 {
				fmt.Println("  (none published yet)")
			}
			for _, tt := range mine {
				latest := "(no active versions)"
				for i := len(tt.Versions) - 1; i >= 0; i-- {
					if !tt.Versions[i].Yanked {
						latest = tt.Versions[i].Version
						break
					}
				}
				fmt.Printf("  %-36s  %s\n", tt.ID, latest)
			}

			return nil
		},
	}
}

func userCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "user <id>",
		Short: "Show another author's profile and timetables",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			authorID := args[0]
			client := newClient()

			author, err := client.GetAuthor(authorID)
			if err != nil {
				return err
			}

			name := author.DisplayName
			if name == "" {
				name = "(no display name)"
			}
			fmt.Printf("%-12s %s\n", "ID:", author.ID)
			fmt.Printf("%-12s %s\n", "Name:", name)
			fmt.Printf("%-12s %s\n", "Public key:", author.PublicKey)

			result, err := client.ListTimetables(1, 100)
			if err != nil {
				return err
			}

			var theirs []Timetable
			for _, tt := range result.Timetables {
				if tt.Publisher == authorID {
					theirs = append(theirs, tt)
				}
			}

			fmt.Printf("\nTimetables (%d):\n", len(theirs))
			if len(theirs) == 0 {
				fmt.Println("  (none)")
			}
			for _, tt := range theirs {
				latest := "(no active versions)"
				for i := len(tt.Versions) - 1; i >= 0; i-- {
					if !tt.Versions[i].Yanked {
						latest = tt.Versions[i].Version
						break
					}
				}
				fmt.Printf("  %-36s  %s\n", tt.ID, latest)
			}

			return nil
		},
	}
}

func verifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <archive>",
		Short: "Verify a timetable archive's signature against the registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			archivePath := args[0]

			archiveData, err := os.ReadFile(archivePath)
			if err != nil {
				return fmt.Errorf("read %s: %w", archivePath, err)
			}

			tgz, err := openTarGz(archiveData)
			if err != nil {
				return fmt.Errorf("invalid archive: %w", err)
			}

			jsonData, err := readTarEntry(tgz, "timetable.json")
			if err != nil {
				return fmt.Errorf("missing timetable.json in archive: %w", err)
			}

			var doc struct {
				Manifest *Manifest `json:"manifest"`
			}
			if err := json.Unmarshal(jsonData, &doc); err != nil {
				return fmt.Errorf("parse timetable.json: %w", err)
			}
			if doc.Manifest == nil {
				return fmt.Errorf("timetable.json has no manifest section")
			}
			m := doc.Manifest

			authorID := m.Manifest.Author
			fmt.Printf("Claimed author:  %s\n", authorID)
			fmt.Printf("Timetable ID:    %s\n", m.Manifest.ID)
			fmt.Printf("Version:         %s\n", m.Manifest.Version)
			fmt.Println()

			fmt.Printf("Fetching public key for %q ...\n", authorID)
			client := newClient()
			author, err := client.GetAuthor(authorID)
			if err != nil {
				return fmt.Errorf("could not fetch author from registry: %w", err)
			}
			if author.PublicKey == "" {
				return fmt.Errorf("registry returned empty public key for %q — server may not expose public keys", authorID)
			}

			hash, err := computeHashFromTarGz(tgz)
			if err != nil {
				return fmt.Errorf("compute hash: %w", err)
			}

			if err := verifySignature(tgz, author.PublicKey, hash); err != nil {
				fmt.Println("✗ Signature INVALID")
				fmt.Printf("  %v\n", err)
				fmt.Println("\nThis archive may have been tampered with or was not signed by the claimed author.")
				os.Exit(1)
			}

			fmt.Println("✓ Signature valid")
			fmt.Printf("  Signed by %q (%s)\n", authorID, author.DisplayName)
			return nil
		},
	}
}

func listCmd() *cobra.Command {
	var page int
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "Browse timetables on the registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			result, err := client.ListTimetables(page, limit)
			if err != nil {
				return err
			}

			if len(result.Timetables) == 0 {
				fmt.Println("No timetables found.")
				return nil
			}

			fmt.Printf("%-36s  %-12s  %-16s  %s\n", "ID", "Latest", "Publisher", "Name")
			fmt.Println(strings.Repeat("─", 80))
			for _, tt := range result.Timetables {
				latest := "-"
				for i := len(tt.Versions) - 1; i >= 0; i-- {
					if !tt.Versions[i].Yanked {
						latest = tt.Versions[i].Version
						break
					}
				}
				fmt.Printf("%-36s  %-12s  %-16s  %s\n", tt.ID, latest, tt.Publisher, tt.Name)
			}
			fmt.Printf("\nPage %d\n", result.Page)
			return nil
		},
	}

	cmd.Flags().IntVar(&page, "page", 1, "Page number")
	cmd.Flags().IntVar(&limit, "limit", 20, "Results per page (max 100)")
	return cmd
}

func downloadCmd() *cobra.Command {
	var outFile string

	cmd := &cobra.Command{
		Use:   "download <id> <version>",
		Short: "Download a timetable archive from the registry",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, version := args[0], args[1]

			client := newClient()
			fmt.Printf("Downloading %s@%s ...\n", id, version)
			data, err := client.DownloadTimetable(id, version)
			if err != nil {
				return err
			}

			if outFile == "" {
				outFile = fmt.Sprintf("%s-%s.tar.gz", id, version)
			}
			if err := os.WriteFile(outFile, data, 0644); err != nil {
				return fmt.Errorf("write %s: %w", outFile, err)
			}

			fmt.Printf("✓ Saved to %s (%.1f KB)\n", outFile, float64(len(data))/1024)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outFile, "output", "o", "", "Output path (default: <id>-<version>.zip)")
	return cmd
}

func mapCmd() *cobra.Command {
	var dataPath string

	cmd := &cobra.Command{
		Use:   "map [path]",
		Short: "Open the track visualiser",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				dataPath = args[0]
			}
			if dataPath == "" {
				exePath, err := os.Executable()
				if err != nil {
					return fmt.Errorf("get executable path: %w", err)
				}
				dataPath = filepath.Join(filepath.Dir(exePath), "data", "kestby.toml")
			}

			layout, err := openFile(dataPath)
			if err != nil {
				return fmt.Errorf("load %s: %w", dataPath, err)
			}

			a := app.New()

			w := a.NewWindow("Interlocked SDK")
			w.Resize(fyne.NewSize(1100, 800))

			status := canvas.NewText("Loading...", color.NRGBA{
				R: 0xc8,
				G: 0xcc,
				B: 0xd1,
				A: 0xff,
			})
			status.TextSize = 12

			statusBar := container.NewPadded(status)

			mapWidget := NewMapWidget(func(s string) {
				status.Text = s
				status.Refresh()
			})

			mapWidget.SetData(layout)

			content := container.NewBorder(
				nil,
				statusBar,
				nil,
				nil,
				mapWidget,
			)

			w.SetContent(content)
			w.ShowAndRun()
			return nil
		},
	}

	cmd.Flags().StringVarP(&dataPath, "data", "d", "", "Track data file (default: ./data/kestby.toml relative to binary)")
	return cmd
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(strings.ToLower(line)) == "y"
}

func init() {
	_ = filepath.Clean
}
