global = {
  go_ver    = "1.27"
  root_path = "root.sh"
  script    = "child/child.sh"
}
local = {
  version = "1.27"
}
target = {
  "//child/atte.hcl#test.0" = {
    file   = "child/atte.hcl"
    index  = 0
    inline = "echo inline\n"
    kind   = "attehcl:test"
    label  = "child"
    name   = "child"
    script = ""
  }
  "//child/atte.hcl#test.1" = {
    file   = "child/atte.hcl"
    index  = 1
    inline = ""
    kind   = "attehcl:test"
    label  = "child2"
    name   = "child2"
    script = "child/child.sh"
  }
}
