target = {
  "//child/atte.hcl#test.child" = {
    file   = "child/atte.hcl"
    index  = 0
    inline = "echo inline\n"
    kind   = "attehcl:test"
    label  = "child"
    name   = "child"
    script = ""
  }
  "//child/atte.hcl#test.child2" = {
    file   = "child/atte.hcl"
    index  = 1
    inline = ""
    kind   = "attehcl:test"
    label  = "child2"
    name   = "child2"
    script = "child/child.sh"
  }
}
