test "go" {
  script     = "./test.sh"
  depends_on = ["//go.mod"]
}

codegen "go" {
  script = "./codegen.sh"
}

lint "go" {
  script = "./lint.sh"
}
