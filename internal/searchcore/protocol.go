package searchcore

import "fmt"

const ProtocolVersion = 1

func CheckProtocolVersion(version int) error {
	if version != ProtocolVersion {
		return fmt.Errorf("不支持的协议版本: %d", version)
	}
	return nil
}

type HelperRequest struct {
	Version int          `json:"version"`
	Op      string       `json:"op"`
	Grep    *GrepOptions `json:"grep,omitempty"`
	Find    *FindOptions `json:"find,omitempty"`
}

type HelperResponse struct {
	Version int         `json:"version"`
	Grep    *GrepResult `json:"grep,omitempty"`
	Find    *FindResult `json:"find,omitempty"`
	Error   string      `json:"error,omitempty"`
}
