package stmanager

import (
	"os"
	"path/filepath"
	"testing"
)

// A trimmed but realistic Syncthing config.xml — folder id/path/type live as
// attributes on <folder>, which is what readFolders parses. One folder omits
// the type attribute to pin the "empty ⇒ ST default" behaviour the gate relies
// on (empty must NOT read as sendonly, or a backstop tick could wrongly skip).
const sampleConfig = `<configuration version="37">
    <folder id="photos" label="Photos" path="/storage/emulated/0/Photos" type="sendonly" rescanIntervalS="3600">
        <device id="AAA"></device>
    </folder>
    <folder id="docs" label="Docs" path="/storage/emulated/0/Docs" type="sendreceive">
        <device id="BBB"></device>
    </folder>
    <folder id="legacy" label="Legacy" path="/storage/emulated/0/Legacy">
        <device id="CCC"></device>
    </folder>
    <gui enabled="true">
        <address>127.0.0.1:8385</address>
        <apikey>secret</apikey>
    </gui>
</configuration>`

func TestReadFolders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.xml")
	if err := os.WriteFile(path, []byte(sampleConfig), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	folders, err := readFolders(path)
	if err != nil {
		t.Fatalf("readFolders: %v", err)
	}
	if len(folders) != 3 {
		t.Fatalf("want 3 folders, got %d: %+v", len(folders), folders)
	}

	want := map[string]struct {
		path, typ string
	}{
		"photos": {"/storage/emulated/0/Photos", "sendonly"},
		"docs":   {"/storage/emulated/0/Docs", "sendreceive"},
		"legacy": {"/storage/emulated/0/Legacy", ""}, // no type attr ⇒ empty
	}
	for _, f := range folders {
		w, ok := want[f.ID]
		if !ok {
			t.Errorf("unexpected folder %q", f.ID)
			continue
		}
		if f.Path != w.path {
			t.Errorf("%s: path = %q, want %q", f.ID, f.Path, w.path)
		}
		if f.Type != w.typ {
			t.Errorf("%s: type = %q, want %q", f.ID, f.Type, w.typ)
		}
	}
}

func TestReadFolders_MissingFile(t *testing.T) {
	if _, err := readFolders(filepath.Join(t.TempDir(), "nope.xml")); err == nil {
		t.Errorf("expected an error reading a missing config.xml")
	}
}
