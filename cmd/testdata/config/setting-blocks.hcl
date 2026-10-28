target = {
  "//child/atte.hcl#test.child" = {
    file   = "child/atte.hcl"
    id     = "attehcl:test:child/atte.hcl:child"
    index  = 0
    inline = "echo child"
    kind   = "attehcl:test"
    label  = "child"
    name   = "child"
    script = ""
  }
}
settings = {
  "//child/atte.hcl#settings" = {
    file = "child/atte.hcl"
    name = "settings"
    values = {
      flags = ["-e", "-u"]
      shell = "bash"
    }
  }
}
