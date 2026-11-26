//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const inheritOnlyACE = 0x08

func validateDataDirACL(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read security descriptor: %w", err)
	}

	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current user SID: %w", err)
	}
	if owner, _, err := sd.Owner(); err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		if err != nil {
			return fmt.Errorf("read directory owner: %w", err)
		}
		return fmt.Errorf("directory is not owned by the current user")
	}

	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		if err != nil {
			return fmt.Errorf("read directory DACL: %w", err)
		}
		return fmt.Errorf("directory has a null DACL")
	}

	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve SYSTEM SID: %w", err)
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("resolve Administrators SID: %w", err)
	}
	creatorSID, err := windows.CreateWellKnownSid(windows.WinCreatorOwnerSid)
	if err != nil {
		return fmt.Errorf("resolve CREATOR OWNER SID: %w", err)
	}

	hasUserAccess := false
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("read directory ACL entry: %w", err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("directory ACL contains an unsupported access entry")
		}

		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(user.User.Sid):
			hasUserAccess = true
		case sid.Equals(systemSID), sid.Equals(adminSID):
		case sid.Equals(creatorSID) && ace.Header.AceFlags&inheritOnlyACE != 0:
		default:
			return fmt.Errorf("directory ACL grants access to an unexpected account")
		}
	}
	if !hasUserAccess {
		return fmt.Errorf("directory ACL does not grant access to the current user")
	}
	return nil
}
