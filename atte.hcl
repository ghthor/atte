test "go" {
  script     = "./atte_test.sh"
  depends_on = ["//go.mod"]
}

test "build" {
  script     = "./atte_build.sh"
  depends_on = ["//go.mod"]
}

codegen "fmt" {
  script = "./atte_fmt.sh"
}

codegen "go" {
  script = "./atte_codegen.sh"
}

lint "go" {
  script = "./atte_lint.sh"
}
