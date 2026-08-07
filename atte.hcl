test "go" {
  script     = path("./atte_test.sh")
  depends_on = ["//go.mod"]
}

test "build" {
  script     = path("./atte_build.sh")
  depends_on = ["//go.mod"]
}

codegen "fmt" {
  script = path("./atte_fmt.sh")
}

codegen "go" {
  script = path("./atte_codegen.sh")
  depends_on = [
    gopkg("./cmd"),
  ]
}

codegen "rendered" {
  script = path("./atte_codegen_rendered.sh")
}

lint "go" {
  script = path("./atte_lint.sh")
}
