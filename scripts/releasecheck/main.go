// Command releasecheck packages plugin release binaries the way Silo stores
// them and refuses a release whose pieces disagree.
//
// Silo starts a plugin only when the manifest it installed equals, by
// proto.Equal, the manifest the running binary reports (pluginhost launch
// guard), and it accepts an archive only when manifest.json's checksum is the
// sha256 of its plugin binary. A hand-built archive that broke the first rule
// stopped Pastime tracking for most of a day, so every manifest here is
// generated from the binary and checked against it.
//
// Usage (the Makefile's release and release-check targets):
//
//	go run ./scripts/releasecheck -dir bin/release-0.2.0 -version 0.2.0 -platforms "linux/amd64 linux/arm64 darwin/arm64" [-package] [-catalog path/to/bloem-plugins/manifest.json]
//
// The catalog is a different format: Silo decodes the whole bloem-plugins
// index with encoding/json into the generated manifest struct, so catalog
// manifests use snake_case keys and enum numbers, and one entry Silo cannot
// decode hides every plugin in the catalog. -package therefore also writes
// catalog-entry.json, the entry to publish, generated the same way.
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type release struct {
	dir, version string
	platforms    []string
	native       string
	printed      []byte             // the native binary's own manifest output
	manifest     *v1.PluginManifest // parsed from printed
	binaries     map[string][]byte  // platform -> plugin binary
	checksums    map[string]string  // platform -> sha256 hex
}

func main() {
	var r release
	var platforms, source, mainGo, catalog string
	var pack bool
	flag.StringVar(&r.dir, "dir", "", "release directory with plugin-<os>-<arch> binaries")
	flag.StringVar(&r.version, "version", "", "release version")
	flag.StringVar(&platforms, "platforms", "", "space-separated os/arch list")
	flag.StringVar(&source, "source", "cmd/bloem-pastime/manifest.json", "embedded manifest source")
	flag.StringVar(&mainGo, "main", "cmd/bloem-pastime/main.go", "file declaring the default version")
	flag.StringVar(&catalog, "catalog", "", "optional bloem-plugins manifest.json to check")
	flag.BoolVar(&pack, "package", false, "write per-platform manifests and archives before checking")
	flag.Parse()
	r.platforms = strings.Fields(platforms)
	r.native = runtime.GOOS + "/" + runtime.GOARCH
	err := r.load(source, mainGo)
	if err == nil && pack {
		err = r.pack()
	}
	if err == nil {
		err = r.checkArchives()
	}
	if err == nil {
		err = r.checkCatalogEntry()
	}
	if err == nil && catalog != "" {
		err = r.checkCatalog(catalog)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release check failed:", err)
		os.Exit(1)
	}
	fmt.Printf("release check passed: %s %s for %s\n", r.manifest.GetPluginId(), r.version, strings.Join(r.platforms, ", "))
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func name(platform string) string { return strings.ReplaceAll(platform, "/", "-") }

// load reads the binaries and checks versions, platforms and checksums.txt.
func (r *release) load(source, mainGo string) error {
	if r.dir == "" || r.version == "" || len(r.platforms) == 0 {
		return errors.New("-dir, -version and -platforms are required")
	}
	if !slices.Contains(r.platforms, r.native) {
		return fmt.Errorf("the native platform %s must be part of the release so its binary can report the manifest", r.native)
	}
	// Versions agree: Makefile (passed as -version), embedded manifest, main.go.
	src := &v1.PluginManifest{}
	if b, err := os.ReadFile(source); err != nil || protojson.Unmarshal(b, src) != nil {
		return fmt.Errorf("read %s", source)
	}
	if src.GetVersion() != r.version {
		return fmt.Errorf("%s says version %s, release is %s", source, src.GetVersion(), r.version)
	}
	b, err := os.ReadFile(mainGo)
	if err != nil {
		return err
	}
	if m := regexp.MustCompile(`(?m)^var version = "([^"]*)"`).FindSubmatch(b); m == nil || string(m[1]) != r.version {
		return fmt.Errorf("%s default version differs from %s", mainGo, r.version)
	}
	r.binaries, r.checksums = map[string][]byte{}, map[string]string{}
	for _, platform := range r.platforms {
		b, err := os.ReadFile(filepath.Join(r.dir, "plugin-"+name(platform)))
		if err != nil {
			return err
		}
		r.binaries[platform], r.checksums[platform] = b, sum(b)
	}
	if err := r.checkChecksums(); err != nil {
		return err
	}
	// The native binary reports the manifest every archive derives from.
	r.printed, err = exec.Command(filepath.Join(r.dir, "plugin-"+name(r.native)), "manifest").Output()
	if err != nil {
		return fmt.Errorf("run the %s binary's manifest command: %w", r.native, err)
	}
	r.manifest = &v1.PluginManifest{}
	if err := protojson.Unmarshal(r.printed, r.manifest); err != nil {
		return fmt.Errorf("parse the binary's manifest: %w", err)
	}
	if r.manifest.GetVersion() != r.version || r.manifest.GetChecksum() != r.checksums[r.native] {
		return fmt.Errorf("binary manifest version %s checksum %s", r.manifest.GetVersion(), r.manifest.GetChecksum())
	}
	var supported []string
	for _, p := range r.manifest.GetSupportedPlatforms() {
		supported = append(supported, p.GetOs()+"/"+p.GetArch())
	}
	if !sameSet(supported, r.platforms) {
		return fmt.Errorf("supported_platforms %v differ from the release platforms %v", supported, r.platforms)
	}
	return nil
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func (r *release) checkChecksums() error {
	f, err := os.Open(filepath.Join(r.dir, "checksums.txt"))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	listed := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return fmt.Errorf("checksums.txt line %q", scanner.Text())
		}
		listed[strings.TrimPrefix(fields[1], "*")] = fields[0]
	}
	if len(listed) != len(r.platforms) {
		return fmt.Errorf("checksums.txt lists %d files for %d platforms", len(listed), len(r.platforms))
	}
	for _, platform := range r.platforms {
		if listed["plugin-"+name(platform)] != r.checksums[platform] {
			return fmt.Errorf("checksums.txt disagrees with plugin-%s", name(platform))
		}
	}
	return scanner.Err()
}

// expected is the manifest Silo must install for one platform's binary: the
// same embedded manifest, stamped with that binary's checksum.
func (r *release) expected(platform string) *v1.PluginManifest {
	m := proto.Clone(r.manifest).(*v1.PluginManifest)
	m.Checksum = r.checksums[platform]
	return m
}

// pack writes manifest-<os>-<arch>.json and bloem-pastime-<version>-<os>-<arch>.zip
// (manifest.json then plugin, Silo's archive layout). The native manifest is
// the binary's output byte for byte; the others differ only in checksum.
func (r *release) pack() error {
	for _, platform := range r.platforms {
		manifest := r.printed
		if platform != r.native {
			b, err := protojson.Marshal(r.expected(platform))
			if err != nil {
				return err
			}
			manifest = append(b, '\n')
		}
		if err := os.WriteFile(filepath.Join(r.dir, "manifest-"+name(platform)+".json"), manifest, 0o644); err != nil {
			return err
		}
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		for _, entry := range []struct {
			name string
			mode os.FileMode
			data []byte
		}{{"manifest.json", 0o644, manifest}, {"plugin", 0o755, r.binaries[platform]}} {
			h := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
			h.SetMode(entry.mode)
			f, err := w.CreateHeader(h)
			if err != nil {
				return err
			}
			if _, err = f.Write(entry.data); err != nil {
				return err
			}
		}
		if err := w.Close(); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(r.dir, archiveName(r.version, platform)), buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	entry, err := r.catalogEntry()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, catalogEntryFile), entry, 0o644)
}

const (
	catalogEntryFile = "catalog-entry.json"
	// releaseBase is where bloem-plugins publishes the release files, as for
	// every earlier Pastime release (tag pastime-v<version>).
	releaseBase = "https://github.com/Bloem-Studios/bloem-plugins/releases/download/pastime-v"
	repoURL     = "https://github.com/Bloem-Studios/bloem-plugins"
)

// catalogPackage is one plugins[] entry of the bloem-plugins index, with the
// fields Silo reads (internal/plugins/catalog_service.go CatalogPackage).
type catalogPackage struct {
	Manifest     *v1.PluginManifest       `json:"manifest"`
	RepoURL      string                   `json:"repo_url,omitempty"`
	ArchiveURL   string                   `json:"archive_url,omitempty"`
	ChecksumsURL string                   `json:"checksums_url,omitempty"`
	Binaries     map[string]catalogBinary `json:"binaries,omitempty"`
}

type catalogBinary struct {
	URL      string `json:"url"`
	Checksum string `json:"checksum"`
}

// catalogEntry is the release's catalog entry in Silo's wire format: the
// binary's manifest through encoding/json, carrying the linux/amd64 checksum
// as earlier entries do, and one binary per platform.
func (r *release) catalogEntry() ([]byte, error) {
	stamp := "linux/amd64"
	if _, ok := r.checksums[stamp]; !ok {
		stamp = r.native
	}
	entry := catalogPackage{
		Manifest:     r.expected(stamp),
		RepoURL:      repoURL,
		ChecksumsURL: releaseBase + r.version + "/checksums.txt",
		Binaries:     map[string]catalogBinary{},
	}
	for _, platform := range r.platforms {
		entry.Binaries[platform] = catalogBinary{URL: releaseBase + r.version + "/plugin-" + name(platform), Checksum: r.checksums[platform]}
	}
	b, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// checkCatalogEntry checks the generated catalog-entry.json, when present, as
// a one-entry catalog.
func (r *release) checkCatalogEntry() error {
	b, err := os.ReadFile(filepath.Join(r.dir, catalogEntryFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.checkCatalogBytes(append(append([]byte(`{"plugins":[`), b...), ']', '}'))
}

func archiveName(version, platform string) string {
	return "bloem-pastime-" + version + "-" + name(platform) + ".zip"
}

// checkArchives opens each archive as Silo does and compares its manifest
// with the binary inside it.
func (r *release) checkArchives() error {
	for _, platform := range r.platforms {
		files, err := readZip(filepath.Join(r.dir, archiveName(r.version, platform)))
		if err != nil {
			return err
		}
		manifest, binary := files["manifest.json"], files["plugin"]
		if len(files) != 2 || manifest == nil || binary == nil {
			return fmt.Errorf("%s must hold exactly manifest.json and plugin", platform)
		}
		if !bytes.Equal(binary, r.binaries[platform]) {
			return fmt.Errorf("%s archive binary differs from plugin-%s", platform, name(platform))
		}
		loose, err := os.ReadFile(filepath.Join(r.dir, "manifest-"+name(platform)+".json"))
		if err != nil || !bytes.Equal(loose, manifest) {
			return fmt.Errorf("manifest-%s.json differs from the archive's manifest.json", name(platform))
		}
		parsed := &v1.PluginManifest{}
		if err := protojson.Unmarshal(manifest, parsed); err != nil {
			return fmt.Errorf("%s manifest.json: %w", platform, err)
		}
		if parsed.GetChecksum() != sum(binary) || !proto.Equal(parsed, r.expected(platform)) {
			return fmt.Errorf("%s manifest.json does not describe its binary", platform)
		}
		if platform == r.native {
			// Run the archived binary itself: its manifest output must be
			// the archive's manifest.json, byte for byte and by proto.Equal.
			printed, err := runManifest(binary)
			live := &v1.PluginManifest{}
			if err != nil || protojson.Unmarshal(printed, live) != nil || !bytes.Equal(printed, manifest) || !proto.Equal(live, parsed) {
				return fmt.Errorf("%s binary's manifest output differs from the archive's manifest.json", platform)
			}
		}
	}
	return nil
}

func readZip(path string) (map[string][]byte, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = z.Close() }()
	files := map[string][]byte{}
	for _, f := range z.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		files[f.Name] = b
	}
	return files, nil
}

func runManifest(binary []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "releasecheck-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "plugin")
	if err := os.WriteFile(path, binary, 0o755); err != nil {
		return nil, err
	}
	return exec.Command(path, "manifest").Output()
}

// checkCatalog compares a bloem-plugins catalog entry with the release: its
// manifest equals the binary's apart from the per-binary checksum, and each
// listed binary checksum is the file's sha256 and the checksums.txt line.
func (r *release) checkCatalog(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return r.checkCatalogBytes(b)
}

// checkCatalogBytes decodes the whole index exactly as Silo does. protojson
// would also accept camelCase keys and enum names, which Silo's encoding/json
// drops or refuses for the entire catalog.
func (r *release) checkCatalogBytes(b []byte) error {
	var index struct {
		Plugins []catalogPackage `json:"plugins"`
	}
	if err := json.Unmarshal(b, &index); err != nil {
		return fmt.Errorf("the catalog does not decode the way Silo reads it (encoding/json): %w", err)
	}
	for _, entry := range index.Plugins {
		m := entry.Manifest
		if m.GetPluginId() != r.manifest.GetPluginId() || m.GetVersion() != r.version {
			continue
		}
		want := proto.Clone(r.manifest).(*v1.PluginManifest)
		got := proto.Clone(m).(*v1.PluginManifest)
		want.Checksum, got.Checksum = "", ""
		if !proto.Equal(got, want) {
			return errors.New("catalog manifest, as Silo decodes it, differs from the binary's manifest")
		}
		var listed []string
		for platform, binary := range entry.Binaries {
			listed = append(listed, platform)
			if binary.Checksum != r.checksums[platform] {
				return fmt.Errorf("catalog checksum for %s differs from the release binary", platform)
			}
			if binary.URL == "" {
				return fmt.Errorf("catalog lists no URL for %s", platform)
			}
		}
		if !sameSet(listed, r.platforms) {
			return fmt.Errorf("catalog platforms %v differ from %v", listed, r.platforms)
		}
		return nil
	}
	return fmt.Errorf("catalog has no %s %s entry", r.manifest.GetPluginId(), r.version)
}
