//go:build windows

package scanner

import (
	"strings"
	"syscall"
	"unsafe"

	"diskpulse/internal/model"
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetLogicalDrives   = kernel32.NewProc("GetLogicalDriveStringsW")
	procGetDriveType       = kernel32.NewProc("GetDriveTypeW")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetVolumeInfo      = kernel32.NewProc("GetVolumeInformationW")
	procGetCompressedSize  = kernel32.NewProc("GetCompressedFileSizeW")
)

const (
	driveFixed = 3

	fileAttrSparseFile         = 0x00000200
	fileAttrReparsePoint       = 0x00000400
	fileAttrCompressed         = 0x00000800
	fileAttrOffline            = 0x00001000
	fileAttrRecallOnOpen       = 0x00040000
	fileAttrRecallOnDataAccess = 0x00400000
)

// ListFixedDrives enumerates local fixed Windows drives and their free/total bytes.
func ListFixedDrives() ([]model.DriveInfo, error) {
	buf := make([]uint16, 256)
	n, _, err := procGetLogicalDrives.Call(uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	if n == 0 {
		return nil, err
	}

	var drives []model.DriveInfo
	start := 0
	for i := 0; i < int(n); i++ {
		if buf[i] != 0 {
			continue
		}
		if i > start {
			root := syscall.UTF16ToString(buf[start:i])
			if isFixedDrive(root) {
				info, err := GetDriveStats(root)
				if err == nil {
					drives = append(drives, info)
				}
			}
		}
		start = i + 1
	}
	return drives, nil
}

func isFixedDrive(root string) bool {
	ptr, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return false
	}
	t, _, _ := procGetDriveType.Call(uintptr(unsafe.Pointer(ptr)))
	return t == driveFixed
}

// GetDriveStats queries live capacity and volume label for a Windows drive root (e.g. "C:\").
func GetDriveStats(root string) (model.DriveInfo, error) {
	root = strings.TrimSpace(root)
	if len(root) == 2 && root[1] == ':' {
		root += `\`
	}
	if len(root) >= 1 && root[1] == ':' {
		root = strings.ToUpper(root[:1]) + root[1:]
	}
	ptr, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return model.DriveInfo{}, err
	}

	var freeAvail, totalBytes, totalFree uint64
	ret, _, callErr := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&freeAvail)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if ret == 0 {
		return model.DriveInfo{}, callErr
	}

	volBuf := make([]uint16, 128)
	_, _, _ = procGetVolumeInfo.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&volBuf[0])),
		uintptr(len(volBuf)),
		0, 0, 0, 0, 0,
	)
	label := strings.TrimSpace(syscall.UTF16ToString(volBuf))
	if label == "" {
		label = "Local Disk"
	}

	var freePct float64
	if totalBytes > 0 {
		freePct = float64(totalFree) / float64(totalBytes) * 100.0
	}

	return model.DriveInfo{
		Root:       normalizeRoot(root),
		Label:      label,
		TotalBytes: int64(totalBytes),
		FreeBytes:  int64(totalFree),
		UsedBytes:  int64(totalBytes - totalFree),
		FreePct:    freePct,
	}, nil
}

// inspectFileAttributes inspects Windows attributes and real on-disk byte size without triggering cloud downloads.
func inspectFileAttributes(fullPath string, sys any) (skip bool, sizeBytes int64, ok bool) {
	attr, ok := sys.(*syscall.Win32FileAttributeData)
	if !ok || attr == nil {
		return false, 0, false
	}
	attrs := attr.FileAttributes
	if (attrs & fileAttrReparsePoint) != 0 {
		return true, 0, true
	}
	if (attrs & (fileAttrOffline | fileAttrRecallOnOpen | fileAttrRecallOnDataAccess)) != 0 {
		// OneDrive / cloud-only placeholder does not consume local disk bytes.
		return true, 0, true
	}
	logical := int64(attr.FileSizeHigh)<<32 | int64(attr.FileSizeLow)
	if logical > 0 && (attrs&(fileAttrSparseFile|fileAttrCompressed)) != 0 {
		if allocated, err := getCompressedFileSize(fullPath); err == nil {
			return false, allocated, true
		}
	}
	return false, logical, true
}

func isReparseDir(sys any) bool {
	attr, ok := sys.(*syscall.Win32FileAttributeData)
	if !ok || attr == nil {
		return false
	}
	return (attr.FileAttributes & fileAttrReparsePoint) != 0
}

func getCompressedFileSize(path string) (int64, error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var high uint32
	low, _, callErr := procGetCompressedSize.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&high)),
	)
	if low == 0xFFFFFFFF {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return 0, errno
		}
	}
	return int64(high)<<32 | int64(low), nil
}
