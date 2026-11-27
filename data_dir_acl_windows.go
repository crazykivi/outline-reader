//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const inheritOnlyACE = 0x08

func validateDataDirACL(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode directory path: %w", err)
	}
	attrs, err := windows.GetFileAttributes(pathPtr)
	if err != nil {
		return fmt.Errorf("read directory attributes: %w", err)
	}
	if attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("path is not a regular directory")
	}

	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read security descriptor: %w", err)
	}

	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current user SID: %w", err)
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve SYSTEM SID: %w", err)
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("resolve Administrators SID: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("read directory owner: %w", err)
	}
	if owner == nil || (!owner.Equals(user.User.Sid) && !owner.Equals(systemSID) && !owner.Equals(adminSID)) {
		return fmt.Errorf("directory is not owned by a trusted account")
	}

	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		if err != nil {
			return fmt.Errorf("read directory DACL: %w", err)
		}
		return fmt.Errorf("directory has a null DACL")
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
