package cursorbyok

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for _, path := range []string{root, secret} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		acl := sd.String()
		if strings.Contains(acl, ";;;WD)") || strings.Contains(acl, ";;;BU)") || strings.Count(acl, "(A;") != 2 || !strings.Contains(acl, ";;;SY)") || !strings.Contains(acl, ";;;"+user.User.Sid.String()+")") {
			t.Fatalf("%s retained broad or unexpected access: %s", filepath.Base(path), acl)
		}
	}
}
