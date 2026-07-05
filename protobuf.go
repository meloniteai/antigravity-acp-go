package antigravityacp

import (
	"encoding/binary"
	"errors"
	"io"
)

type StepPayload struct {
	ValidityCheck int64
	ToolRun       *ToolRun
	WriteFile     *WriteFileResult
	GrepSearch    *GrepSearchResult
	ViewFile      *ViewFileResult
	ListDirectory *ListDirectoryResult
	UserPrompt    *UserPrompt
	AgentText     *AgentText
	TitleUpdate   *TitleUpdate
}

type ToolRun struct {
	Call           *ToolCall
	TitlePrimary   string
	TitleSecondary string
}

type ToolCall struct {
	CallID        string
	NamePrimary   string
	RawInputJSON  string
	NameSecondary string
}

type WriteFileResult struct {
	Summary string
}

type GrepSearchResult struct {
	Query        string
	IncludeGlob  string
	TextOutput   string
	Hits         []SearchHit
	ShellCommand string
	CwdURI       string
}

type SearchHit struct {
	Field1 string
	Field2 string
	Field3 string
	Field4 string
	Field5 string
}

type ViewFileResult struct {
	FileURI         string
	StartLine       int64
	EndLine         int64
	Content         string
	NextLine        int64
	FileSizeOrTotal int64
}

type ListDirectoryResult struct {
	DirURI  string
	Entries []DirEntry
}

type DirEntry struct {
	Name        string
	IsDirectory int64
	FileSize    int64
}

type UserPrompt struct {
	Text    string
	Content *UserPromptContent
}

type UserPromptContent struct {
	Text string
}

type AgentText struct {
	Text string
}

type TitleUpdate struct {
	Title string
}

type TaskDetails struct {
	TaskID      string
	LogURI      string
	Description string
}

type PermissionInfo struct {
	Kind     string
	Value    string
	Decision int64
}

type ErrorDetails struct {
	Message    string
	Detail     string
	StackTrace string
}

type ProtoReader struct {
	buf []byte
	pos int
}

func NewProtoReader(buf []byte) *ProtoReader {
	return &ProtoReader{buf: buf, pos: 0}
}

func (r *ProtoReader) HasNext() bool {
	return r.pos < len(r.buf)
}

func (r *ProtoReader) Varint() (uint64, error) {
	val, n := binary.Uvarint(r.buf[r.pos:])
	if n <= 0 {
		return 0, errors.New("malformed varint")
	}
	r.pos += n
	return val, nil
}

func (r *ProtoReader) Bytes() ([]byte, error) {
	length, err := r.Varint()
	if err != nil {
		return nil, err
	}
	if r.pos+int(length) > len(r.buf) {
		return nil, io.ErrUnexpectedEOF
	}
	res := r.buf[r.pos : r.pos+int(length)]
	r.pos += int(length)
	return res, nil
}

func (r *ProtoReader) String() (string, error) {
	b, err := r.Bytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *ProtoReader) Skip(wireType uint64) error {
	switch wireType {
	case 0:
		_, err := r.Varint()
		return err
	case 1:
		if r.pos+8 > len(r.buf) {
			return io.ErrUnexpectedEOF
		}
		r.pos += 8
		return nil
	case 2:
		length, err := r.Varint()
		if err != nil {
			return err
		}
		if r.pos+int(length) > len(r.buf) {
			return io.ErrUnexpectedEOF
		}
		r.pos += int(length)
		return nil
	case 5:
		if r.pos+4 > len(r.buf) {
			return io.ErrUnexpectedEOF
		}
		r.pos += 4
		return nil
	default:
		return errors.New("unknown wire type")
	}
}

func DecodeStepPayload(data []byte) (*StepPayload, error) {
	r := NewProtoReader(data)
	payload := &StepPayload{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 1:
			val, err := r.Varint()
			if err != nil {
				return nil, err
			}
			payload.ValidityCheck = int64(val)
		case 5:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			tr, err := DecodeToolRun(b)
			if err != nil {
				return nil, err
			}
			payload.ToolRun = tr
		case 10:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			wf, err := DecodeWriteFileResult(b)
			if err != nil {
				return nil, err
			}
			payload.WriteFile = wf
		case 13:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			gs, err := DecodeGrepSearchResult(b)
			if err != nil {
				return nil, err
			}
			payload.GrepSearch = gs
		case 14:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			vf, err := DecodeViewFileResult(b)
			if err != nil {
				return nil, err
			}
			payload.ViewFile = vf
		case 15:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			ld, err := DecodeListDirectoryResult(b)
			if err != nil {
				return nil, err
			}
			payload.ListDirectory = ld
		case 19:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			up, err := DecodeUserPrompt(b)
			if err != nil {
				return nil, err
			}
			payload.UserPrompt = up
		case 20:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			at, err := DecodeAgentText(b)
			if err != nil {
				return nil, err
			}
			payload.AgentText = at
		case 30:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			tu, err := DecodeTitleUpdate(b)
			if err != nil {
				return nil, err
			}
			payload.TitleUpdate = tu
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return payload, nil
}

func DecodeToolRun(data []byte) (*ToolRun, error) {
	r := NewProtoReader(data)
	tr := &ToolRun{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 4:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			call, err := DecodeToolCall(b)
			if err != nil {
				return nil, err
			}
			tr.Call = call
		case 30:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			tr.TitlePrimary = s
		case 31:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			tr.TitleSecondary = s
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return tr, nil
}

func DecodeToolCall(data []byte) (*ToolCall, error) {
	r := NewProtoReader(data)
	tc := &ToolCall{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 1:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			tc.CallID = s
		case 2:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			tc.NamePrimary = s
		case 3:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			tc.RawInputJSON = s
		case 9:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			tc.NameSecondary = s
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return tc, nil
}

func DecodeWriteFileResult(data []byte) (*WriteFileResult, error) {
	r := NewProtoReader(data)
	wf := &WriteFileResult{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 26:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			wf.Summary = s
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return wf, nil
}

func DecodeGrepSearchResult(data []byte) (*GrepSearchResult, error) {
	r := NewProtoReader(data)
	gs := &GrepSearchResult{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 1:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			gs.Query = s
		case 2:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			gs.IncludeGlob = s
		case 3:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			gs.TextOutput = s
		case 4:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			hit, err := DecodeSearchHit(b)
			if err != nil {
				return nil, err
			}
			gs.Hits = append(gs.Hits, *hit)
		case 10:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			gs.ShellCommand = s
		case 11:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			gs.CwdURI = s
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return gs, nil
}

func DecodeSearchHit(data []byte) (*SearchHit, error) {
	r := NewProtoReader(data)
	sh := &SearchHit{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 1:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			sh.Field1 = s
		case 2:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			sh.Field2 = s
		case 3:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			sh.Field3 = s
		case 4:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			sh.Field4 = s
		case 5:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			sh.Field5 = s
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return sh, nil
}

func DecodeViewFileResult(data []byte) (*ViewFileResult, error) {
	r := NewProtoReader(data)
	vf := &ViewFileResult{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 1:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			vf.FileURI = s
		case 2:
			val, err := r.Varint()
			if err != nil {
				return nil, err
			}
			vf.StartLine = int64(val)
		case 3:
			val, err := r.Varint()
			if err != nil {
				return nil, err
			}
			vf.EndLine = int64(val)
		case 4:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			vf.Content = s
		case 11:
			val, err := r.Varint()
			if err != nil {
				return nil, err
			}
			vf.NextLine = int64(val)
		case 12:
			val, err := r.Varint()
			if err != nil {
				return nil, err
			}
			vf.FileSizeOrTotal = int64(val)
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return vf, nil
}

func DecodeListDirectoryResult(data []byte) (*ListDirectoryResult, error) {
	r := NewProtoReader(data)
	ld := &ListDirectoryResult{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 1:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			ld.DirURI = s
		case 3:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			entry, err := DecodeDirEntry(b)
			if err != nil {
				return nil, err
			}
			ld.Entries = append(ld.Entries, *entry)
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return ld, nil
}

func DecodeDirEntry(data []byte) (*DirEntry, error) {
	r := NewProtoReader(data)
	de := &DirEntry{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 1:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			de.Name = s
		case 2:
			val, err := r.Varint()
			if err != nil {
				return nil, err
			}
			de.IsDirectory = int64(val)
		case 4:
			val, err := r.Varint()
			if err != nil {
				return nil, err
			}
			de.FileSize = int64(val)
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return de, nil
}

func DecodeUserPrompt(data []byte) (*UserPrompt, error) {
	r := NewProtoReader(data)
	up := &UserPrompt{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 2:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			up.Text = s
		case 3:
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			content, err := DecodeUserPromptContent(b)
			if err != nil {
				return nil, err
			}
			up.Content = content
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return up, nil
}

func DecodeUserPromptContent(data []byte) (*UserPromptContent, error) {
	r := NewProtoReader(data)
	upc := &UserPromptContent{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 1:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			upc.Text = s
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return upc, nil
}

func DecodeAgentText(data []byte) (*AgentText, error) {
	r := NewProtoReader(data)
	at := &AgentText{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 1:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			at.Text = s
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return at, nil
}

func DecodeTitleUpdate(data []byte) (*TitleUpdate, error) {
	r := NewProtoReader(data)
	tu := &TitleUpdate{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 4:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			tu.Title = s
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return tu, nil
}

func DecodeTaskDetails(data []byte) (*TaskDetails, error) {
	r := NewProtoReader(data)
	td := &TaskDetails{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 1:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			td.TaskID = s
		case 2:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			td.LogURI = s
		case 4:
			s, err := r.String()
			if err != nil {
				return nil, err
			}
			td.Description = s
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return td, nil
}

func DecodePermissions(input []byte) (*PermissionInfo, error) {
	r := NewProtoReader(input)
	var entry []byte
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		if fieldNum == 2 && wireType == 2 {
			b, err := r.Bytes()
			if err != nil {
				return nil, err
			}
			entry = b
		} else {
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	if len(entry) == 0 {
		return nil, nil
	}

	er := NewProtoReader(entry)
	var target []byte
	var decision int64
	for er.HasNext() {
		tag, err := er.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		if fieldNum == 1 && wireType == 2 {
			b, err := er.Bytes()
			if err != nil {
				return nil, err
			}
			target = b
		} else if fieldNum == 2 && wireType == 0 {
			dec, err := er.Varint()
			if err != nil {
				return nil, err
			}
			decision = int64(dec)
		} else {
			if err := er.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}

	out := &PermissionInfo{Decision: decision}
	if len(target) > 0 {
		tr := NewProtoReader(target)
		for tr.HasNext() {
			tag, err := tr.Varint()
			if err != nil {
				return nil, err
			}
			wireType := tag & 7
			fieldNum := tag >> 3

			if fieldNum == 1 && wireType == 2 {
				k, err := tr.String()
				if err != nil {
					return nil, err
				}
				out.Kind = k
			} else if fieldNum == 2 && wireType == 2 {
				v, err := tr.String()
				if err != nil {
					return nil, err
				}
				out.Value = v
			} else {
				if err := tr.Skip(wireType); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

func DecodeErrorDetails(input []byte) (*ErrorDetails, error) {
	r := NewProtoReader(input)
	out := &ErrorDetails{}
	for r.HasNext() {
		tag, err := r.Varint()
		if err != nil {
			return nil, err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		switch fieldNum {
		case 1:
			msg, err := r.String()
			if err != nil {
				return nil, err
			}
			out.Message = msg
		case 2:
			detail, err := r.String()
			if err != nil {
				return nil, err
			}
			out.Detail = detail
		case 3:
			stackTrace, err := r.String()
			if err != nil {
				return nil, err
			}
			out.StackTrace = stackTrace
		default:
			if err := r.Skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
