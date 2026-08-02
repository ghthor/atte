globals {
  go_ver = "1.26.5"
}

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
}

lint "go" {
  script = path("./atte_lint.sh")
}
