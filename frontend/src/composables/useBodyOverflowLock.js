// Shared body-overflow lock with reference counting.
//
// Problem: each overlay used to save document.body.style.overflow at mount and
// restore it at unmount. When overlays overlap (one opens while another is
// still in its close animation), the second saves the in-flight 'hidden' as
// its "previous" value and restores it on unmount, leaving the body locked
// forever. Counting acquires keeps the lock open exactly while at least one
// overlay needs it.

let lockCount = 0
let savedOverflow = ''

export function lockBodyOverflow() {
  if (typeof document === 'undefined') return
  if (lockCount === 0) {
    savedOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
  }
  lockCount++
}

export function unlockBodyOverflow() {
  if (typeof document === 'undefined') return
  lockCount--
  if (lockCount <= 0) {
    lockCount = 0
    document.body.style.overflow = savedOverflow
  }
}
