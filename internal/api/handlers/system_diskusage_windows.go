//go:build windows

package handlers

import "golang.org/x/sys/windows"

// diskUsage returns the available and total bytes of the volume that contains
// path. ok is false if the figures could not be determined.
func diskUsage(path string) (free, total uint64, ok bool) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}
	// totalFree (totalNumberOfFreeBytes) is required by the API but unused here;
	// freeAvail is the caller-quota-adjusted free space we want to report.
	var freeAvail, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeAvail, &totalBytes, &totalFree); err != nil {
		return 0, 0, false
	}
	return freeAvail, totalBytes, true
}
