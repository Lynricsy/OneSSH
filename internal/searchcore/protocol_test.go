package searchcore

import (
	"encoding/json"
	"testing"
)

func TestHelperProtocolRoundTrip(t *testing.T) {
	request := HelperRequest{
		Version: ProtocolVersion,
		Op:      "grep",
		Grep:    &GrepOptions{Pattern: "needle", Path: ".", Limit: 10},
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded HelperRequest
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != ProtocolVersion || decoded.Op != "grep" || decoded.Grep == nil || decoded.Grep.Pattern != "needle" || decoded.Find != nil {
		t.Fatalf("请求 round-trip 错误: %+v", decoded)
	}

	result := GrepResult{Lines: []GrepLine{{Path: "main.go", Line: 1, Text: "needle", Match: true}}, MatchCount: 1}
	response := HelperResponse{Version: ProtocolVersion, Grep: &result}
	encoded, err = json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decodedResponse HelperResponse
	if err = json.Unmarshal(encoded, &decodedResponse); err != nil {
		t.Fatal(err)
	}
	if decodedResponse.Version != ProtocolVersion || decodedResponse.Grep == nil || decodedResponse.Grep.MatchCount != 1 || decodedResponse.Error != "" {
		t.Fatalf("响应 round-trip 错误: %+v", decodedResponse)
	}
}

func TestHelperProtocolVersionMismatch(t *testing.T) {
	encoded := []byte(`{"version":2,"op":"find","find":{"Pattern":"*.go","Path":".","Limit":1}}`)
	var request HelperRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		t.Fatal(err)
	}
	if err := CheckProtocolVersion(request.Version); err == nil {
		t.Fatal("不匹配的协议版本未被拒绝")
	}
}
