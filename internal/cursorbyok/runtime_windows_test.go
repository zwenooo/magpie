package cursorbyok

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestCursorRuntimePrivateWindowsInheritance(t *testing.T) {
	parent := t.TempDir()
	// Model a shared portable directory: Everyone receives inheritable
	// access. The runtime must remove that access from its own subtree.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "cursor-backend")
	if err := privateRuntimeDir(root); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "token")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, secret} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		dacl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		// Compare the ACEs' SIDs, not SDDL text: SDDL writes some accounts
		// as an alias (the built-in Administrator is "LA"), not their SID.
		private, hasSystem, hasUser := dacl.AceCount == 2, false, false
		for i := range uint32(dacl.AceCount) {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(dacl, i, &ace); err != nil {
				t.Fatal(err)
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			switch {
			case ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE:
				private = false
			case sid.Equals(system):
				hasSystem = true
			case sid.Equals(user.User.Sid):
				hasUser = true
			default:
				private = false
			}
		}
		if !private || !hasSystem || !hasUser {
			t.Fatalf("%s retained broad or unexpected access: %s", filepath.Base(path), sd.String())
		}
	}
}
