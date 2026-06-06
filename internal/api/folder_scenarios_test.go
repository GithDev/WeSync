package api

import (
	"net/http"
	"testing"
)

// setupFolderABC sets up three paired instances and a shared folder:
//   - A and B paired, B and C paired
//   - A shares folder with B → B accepts
//   - B shares folder with C → C accepts
//
// Returns (a, b, c, folderID).
func setupFolderABC(t *testing.T) (a, b, c *instance, folderID string) {
	t.Helper()
	a, b, c = setup3(t)

	// A shares folder with B; get the generated folderID back
	fid, code := doShareFolderID(t, a, idB, "/sync/folder", "TestFolder", "sendreceive")
	if code != http.StatusOK {
		t.Fatalf("A share with B: got %d", code)
	}
	if fid == "" {
		t.Fatal("A share with B: no folderID returned")
	}
	folderID = fid
	assertFolderHasDevice(t, a, fid, idB)

	// Simulate BEP: B sees pending invite from A and accepts
	b.st.addPendingFolder(fid, "TestFolder", idA)
	doFolderAccept(t, b, fid, idA, "/sync/folder-b")
	assertFolderExists(t, b, fid)

	// B shares with C (re-shares the same folder)
	code = doShareFolder(t, b, idC, "/sync/folder-b", "TestFolder", "sendreceive")
	if code != http.StatusOK {
		t.Fatalf("B share with C: got %d", code)
	}
	assertFolderHasDevice(t, b, fid, idC)

	// Simulate BEP: C sees pending invite from B and accepts
	c.st.addPendingFolder(fid, "TestFolder", idB)
	doFolderAccept(t, c, fid, idB, "/sync/folder-c")
	assertFolderExists(t, c, fid)

	return
}

// ── Scenario 1: A→B→C chain — all have the folder ────────────────────────────

func TestFolder_ABCChain_AllHaveFolder(t *testing.T) {
	a, b, c, fid := setupFolderABC(t)

	assertFolderExists(t, a, fid)
	assertFolderExists(t, b, fid)
	assertFolderExists(t, c, fid)

	assertFolderHasDevice(t, a, fid, idB)
	assertFolderHasDevice(t, b, fid, idA)
	assertFolderHasDevice(t, b, fid, idC)
	assertFolderHasDevice(t, c, fid, idB)
}

// ── Scenario 2: C leaves the folder ──────────────────────────────────────────
// A and B keep their copies; C is removed as participant.

func TestFolder_CLeaves_ABKeepFolder_CRemoved(t *testing.T) {
	a, b, c, fid := setupFolderABC(t)

	doFolderLeave(t, c, fid)

	// C no longer has the folder
	assertFolderNotExists(t, c, fid)

	// A and B still have it — we never delete others' folders
	assertFolderExists(t, a, fid)
	assertFolderExists(t, b, fid)

	// B received FolderRemove from C — C no longer listed as participant
	assertFolderMissingDevice(t, b, fid, idC)
}

// ── Scenario 3: A removes C, then re-adds C ───────────────────────────────────

func TestFolder_ARemovesC_ThenReAdds(t *testing.T) {
	a, _, c, fid := setupFolderABC(t)

	// First share folder A→C directly so A can also remove C
	doShareFolder(t, a, idC, "/sync/folder", "TestFolder", "sendreceive")
	assertFolderHasDevice(t, a, fid, idC)

	// A removes C from the folder
	doFolderRemoveDevice(t, a, fid, idC)
	assertFolderMissingDevice(t, a, fid, idC)

	// C receives FolderRemove — C keeps the folder but removes A as participant (folder becomes local-only)
	assertFolderExists(t, c, fid)
	assertFolderMissingDevice(t, c, fid, idA)

	// Re-add: A shares with C again
	doShareFolder(t, a, idC, "/sync/folder", "TestFolder", "sendreceive")
	assertFolderHasDevice(t, a, fid, idC)

	// C sees the new invite (simulate BEP) and accepts (folder already exists, so just adds A back)
	c.st.addPendingFolder(fid, "TestFolder", idA)
	doFolderAccept(t, c, fid, idA, "/sync/folder-c")
	assertFolderHasDevice(t, c, fid, idA)
}

// ── Scenario 4: A pairs directly with C, folder state preserved ───────────────

func TestFolder_APairsWithC_FolderStatePreserved(t *testing.T) {
	a, _, c, fid := setupFolderABC(t)

	// A and C pair directly (previously only connected via B)
	doPair(t, a, idC, "DeviceC")
	doPair(t, c, idA, "DeviceA")

	// Pairing must not touch folder state
	assertFolderExists(t, a, fid)
	assertFolderExists(t, c, fid)
	assertFolderHasDevice(t, a, fid, idB)
	assertFolderHasDevice(t, c, fid, idB)
}

// ── Scenario 5: Kill A-B trust ────────────────────────────────────────────────
// When A removes B:
//   - B removed from A's trusted set
//   - B removed from A's folder (removeDeviceFromAllFolders in untrustDevice)
//   - B keeps its own folder copy

func TestFolder_KillABTrust_BRemovedFromAFolder(t *testing.T) {
	a, b, _, fid := setupFolderABC(t)

	assertFolderHasDevice(t, a, fid, idB) // sanity: B is in A's folder
	assertFolderExists(t, b, fid)         // B has the folder

	doRemoveDevice(t, a, idB)

	assertNoDevice(t, a, idB)               // A no longer trusts B
	assertFolderMissingDevice(t, a, fid, idB) // B removed from A's folder

	// B still has the folder — we never delete a peer's data
	assertFolderExists(t, b, fid)
}
