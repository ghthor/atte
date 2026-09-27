target = {
  "//child/atte.hcl#test.child" = {
    file   = "child/atte.hcl"
    id     = "attehcl:test:child/atte.hcl:child"
    index  = 0
    inline = "echo inline\n"
    kind   = "attehcl:test"
    label  = "child"
    name   = "child"
    script = ""
  }
  "//child/atte.hcl#test.child2" = {
    file   = "child/atte.hcl"
    id     = "attehcl:test:child/atte.hcl:child2"
    index  = 1
    inline = ""
    kind   = "attehcl:test"
    label  = "child2"
    name   = "child2"
    script = "child/child.sh"
  }
}
