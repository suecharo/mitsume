package checker

import "testing"

func TestSuccess_OKTrue(t *testing.T) {
	t.Parallel()
	r := Success()
	if !r.OK {
		t.Fatalf("Success().OK = false")
	}
	if r.Error != "" || r.Observed != "" || r.Expected != "" || r.Stderr != "" {
		t.Fatalf("Success has unexpected content: %+v", r)
	}
}

func TestFailure_FieldsSet(t *testing.T) {
	t.Parallel()
	r := Failure("boom", "obs", "exp")
	if r.OK {
		t.Fatalf("Failure.OK = true")
	}
	if r.Error != "boom" || r.Observed != "obs" || r.Expected != "exp" {
		t.Fatalf("Failure fields: %+v", r)
	}
	if r.Stderr != "" {
		t.Fatalf("Stderr should be empty by default")
	}
}
