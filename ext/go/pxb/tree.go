package pxb

// TreeNavigation is a tagged sub-payload carried by InterceptReq.Input or EventNotify.Input.
// Codes 17/18 use this payload; InterceptResp.Content supplies a summary and Prompt overrides instructions.
type TreeNavigation struct {
	FromID       string
	TargetID     string
	SelectedID   string
	Summary      string
	SummaryID    string
	Instructions string
	Summarize    bool
}

func EncodeTreeNavigation(v TreeNavigation) []byte {
	var w FieldWriter
	w.PutString(1, v.FromID)
	w.PutString(2, v.TargetID)
	w.PutString(3, v.SelectedID)
	w.PutString(4, v.Summary)
	w.PutString(5, v.SummaryID)
	w.PutString(6, v.Instructions)
	w.PutBool(7, v.Summarize)
	return w.Bytes()
}

func DecodeTreeNavigation(b []byte) (TreeNavigation, error) {
	var v TreeNavigation
	fields := []*string{&v.FromID, &v.TargetID, &v.SelectedID, &v.Summary, &v.SummaryID, &v.Instructions}
	err := Walk(b, func(tag uint16, kind uint8, r *FieldReader) error {
		if tag >= 1 && tag <= 6 {
			s, err := takeString(kind, r)
			*fields[tag-1] = s
			return err
		}
		if tag == 7 {
			n, err := takeU64(kind, r)
			v.Summarize = n != 0
			return err
		}
		return r.Skip(kind)
	})
	return v, err
}
