test "go" {
  script     = "./test.sh"
  depends_on = ["//go.mod"]
}
