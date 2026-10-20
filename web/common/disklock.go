package common

import "sync"

// diskOpMu serializes all destructive disk operations across modules
// (wipefs, mdadm create/grow, pvcreate/vgcreate/lvcreate, mkfs, mount/umount
// of data volumes, etc.). Running two of these concurrently against the same
// or overlapping block devices can corrupt data or leave LVM/mdadm metadata
// inconsistent, so every handler that performs such an operation must hold
// this lock for the duration of its destructive steps.
//
// Lock ordering: if a caller also needs another package-level mutex (e.g.
// modules/system's resetMu), it must acquire that other mutex first and
// this one second, to avoid inconsistent ordering across call sites.
var diskOpMu sync.Mutex

// LockDiskOp acquires the global disk-operation lock. Callers must pair this
// with a deferred UnlockDiskOp call immediately after a successful Lock.
func LockDiskOp() {
	diskOpMu.Lock()
}

// UnlockDiskOp releases the global disk-operation lock.
func UnlockDiskOp() {
	diskOpMu.Unlock()
}
