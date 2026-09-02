package isoeditor

// OVEOffsets holds cached offsets for an ISO so we don't have to parse it
type OVEOffsets struct {
	IgnitionOffset int64
	IgnitionLength int64
	Kargs          map[string]OffsetLength
}

type OffsetLength struct {
	Offset int64
	Length int64
}
