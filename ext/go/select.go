package ext

// SelectItem is one option in a host picker.
type SelectItem struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// SelectRequest describes an interactive picker.
type SelectRequest struct {
	Title string       `json:"title"`
	Items []SelectItem `json:"items"`
}

// SelectReply contains the original item index, or OK=false on cancellation.
type SelectReply struct {
	Index int  `json:"index"`
	OK    bool `json:"ok"`
}
