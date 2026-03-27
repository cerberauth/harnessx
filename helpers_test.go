package harnessx

import "testing"

func TestResultData_SetsCheckIDAndData(t *testing.T) {
	r := ResultData("mycheck", 42)
	if r.CheckID != "mycheck" {
		t.Errorf("CheckID: want %q, got %q", "mycheck", r.CheckID)
	}
	if r.Data != 42 {
		t.Errorf("Data: want 42, got %v", r.Data)
	}
}

func TestResultData_NotSkipped(t *testing.T) {
	r := ResultData("c", "payload")
	if r.Skipped {
		t.Error("ResultData result should not be skipped")
	}
}

func TestResultData_StructPayload(t *testing.T) {
	type payload struct{ N int }
	r := ResultData("c", payload{N: 7})
	p, ok := r.Data.(payload)
	if !ok || p.N != 7 {
		t.Errorf("unexpected data: %v", r.Data)
	}
}

func TestSkip_SetsSkippedAndReason(t *testing.T) {
	r := Skip("mycheck", "not applicable")
	if !r.Skipped {
		t.Error("Skipped should be true")
	}
	if r.SkipReason != "not applicable" {
		t.Errorf("SkipReason: want %q, got %q", "not applicable", r.SkipReason)
	}
	if r.CheckID != "mycheck" {
		t.Errorf("CheckID: want %q, got %q", "mycheck", r.CheckID)
	}
}

func TestSkip_EmptyReason(t *testing.T) {
	r := Skip("c", "")
	if !r.Skipped {
		t.Error("Skipped should be true even with empty reason")
	}
	if r.SkipReason != "" {
		t.Errorf("SkipReason should be empty, got %q", r.SkipReason)
	}
}

func TestDataAs_HitsCorrectType(t *testing.T) {
	r := ResultData("c", "hello")
	v, ok := DataAs[string](r)
	if !ok {
		t.Fatal("DataAs should succeed for string")
	}
	if v != "hello" {
		t.Errorf("want %q, got %q", "hello", v)
	}
}

func TestDataAs_WrongType(t *testing.T) {
	r := ResultData("c", 99)
	_, ok := DataAs[string](r)
	if ok {
		t.Error("DataAs should fail when type does not match")
	}
}

func TestDataAs_NilData(t *testing.T) {
	r := Result{CheckID: "c"}
	_, ok := DataAs[string](r)
	if ok {
		t.Error("DataAs should fail when Data is nil")
	}
}

func TestDataAs_ZeroValueOnFailure(t *testing.T) {
	r := Result{CheckID: "c"}
	v, _ := DataAs[int](r)
	if v != 0 {
		t.Errorf("zero value expected, got %d", v)
	}
}

func TestDataAs_StructType(t *testing.T) {
	type info struct{ Code int }
	r := ResultData("c", info{Code: 200})
	v, ok := DataAs[info](r)
	if !ok {
		t.Fatal("DataAs should succeed for struct type")
	}
	if v.Code != 200 {
		t.Errorf("want Code=200, got %d", v.Code)
	}
}

func TestGetData_Found(t *testing.T) {
	store := newStaticStore(ResultData("dep", "value"))
	v, ok := GetData[string](store, "dep")
	if !ok {
		t.Fatal("GetData should succeed")
	}
	if v != "value" {
		t.Errorf("want %q, got %q", "value", v)
	}
}

func TestGetData_CheckNotInStore(t *testing.T) {
	store := newStaticStore()
	v, ok := GetData[string](store, "missing")
	if ok {
		t.Error("GetData should fail for missing check")
	}
	if v != "" {
		t.Errorf("zero value expected, got %q", v)
	}
}

func TestGetData_WrongType(t *testing.T) {
	store := newStaticStore(ResultData("dep", 42))
	_, ok := GetData[string](store, "dep")
	if ok {
		t.Error("GetData should fail when stored type does not match")
	}
}

func TestGetData_NilDataInResult(t *testing.T) {
	store := newStaticStore(Result{CheckID: "dep"})
	_, ok := GetData[string](store, "dep")
	if ok {
		t.Error("GetData should fail when result carries no data")
	}
}

func TestGetData_StructPayload(t *testing.T) {
	type info struct{ Status int }
	store := newStaticStore(ResultData("dep", info{Status: 404}))
	v, ok := GetData[info](store, "dep")
	if !ok {
		t.Fatal("GetData should succeed for struct payload")
	}
	if v.Status != 404 {
		t.Errorf("want Status=404, got %d", v.Status)
	}
}
