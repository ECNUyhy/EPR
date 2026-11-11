package execution

// Diagnostics for offline coverage measurement (d16coverage): read-only access to why program
// extraction gave up. Not used on the endorsement path.

// LostAt reports where the program tracer lost track ("" if it did not).
func (t *EPRProgTracer) LostAt() string { return t.lostAt }
