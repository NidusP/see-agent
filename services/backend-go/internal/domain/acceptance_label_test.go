package domain

import "testing"

func TestAcceptanceDisplayLabel(t *testing.T) {
	cases := []struct {
		snapshot string
		status   string
		want     string
	}{
		{snapshot: "pending_publish", status: "unvalidated", want: "未跑"},
		{snapshot: "pending_publish", status: "accepted", want: "通过"},
		{snapshot: "pending_publish", status: "failed", want: "未通过"},
		{snapshot: "published", status: "accepted", want: "已发布 · 已验收"},
		{snapshot: "published", status: "unvalidated", want: "已发布 · 未验收"},
		{snapshot: "published", status: "failed", want: "已发布 · 验收未通过（仍在服务）"},
		{snapshot: "retired", status: "accepted", want: "已发布 · 已验收"},
		{snapshot: "building", status: "unvalidated", want: ""},
	}
	for _, tc := range cases {
		if got := AcceptanceDisplayLabel(tc.snapshot, tc.status); got != tc.want {
			t.Fatalf("AcceptanceDisplayLabel(%s, %s) = %q, want %q", tc.snapshot, tc.status, got, tc.want)
		}
	}
}
