package selector

import (
	"testing"

	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/shoenig/test"
)

func TestRegisterAndUseMapping(t *testing.T) {
	namespace := graphtarget.Namespace("mapping-test")
	target := graphtarget.ID{Namespace: namespace, Path: "pkg", Kind: "test", Aliases: []string{"test"}}
	match := func(got graphtarget.ID, identifier string) bool {
		return got.Namespace == target.Namespace && got.Path == target.Path && identifier == "test"
	}
	convert := func(got graphtarget.ID) string {
		if got.Namespace == target.Namespace && got.Path == target.Path {
			return "//pkg#test"
		}
		return ""
	}

	test.NoError(t, Register(namespace, match, convert))
	value, ok := String(target)
	test.True(t, ok, test.Sprintf("registered selector mapping should be found"))
	test.EqOp(t, "//pkg#test", value)
	test.True(t, Matches(target, "test", "pkg"), test.Sprintf("registered matcher should match a selector"))
	test.False(t, Matches(target, "other", "pkg"), test.Sprintf("registered matcher should reject another identifier"))
	test.Error(t, Register(namespace, match, convert))
}

func TestRegisterMappingValidation(t *testing.T) {
	match := func(graphtarget.ID, string) bool { return true }
	convert := func(graphtarget.ID) string { return "//#test" }

	test.Error(t, Register("", match, convert))
	test.Error(t, Register("mapping-nil-match", nil, convert))
	test.Error(t, Register("mapping-nil-selector", match, nil))
}
