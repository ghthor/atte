global = {
  go_ver = "1.26.5"
}
local = {}
target = {
  "//atte.hcl#codegen.fmt" = {
    file   = "atte.hcl"
    index  = 0
    inline = ""
    kind   = "attehcl:codegen"
    label  = "fmt"
    name   = "fmt"
    script = "atte_fmt.sh"
  }
  "//atte.hcl#codegen.go" = {
    file   = "atte.hcl"
    index  = 1
    inline = ""
    kind   = "attehcl:codegen"
    label  = "go"
    name   = "go"
    script = "atte_codegen.sh"
  }
  "//atte.hcl#codegen.rendered" = {
    file   = "atte.hcl"
    index  = 2
    inline = ""
    kind   = "attehcl:codegen"
    label  = "rendered"
    name   = "rendered"
    script = "atte_codegen_rendered.sh"
  }
  "//atte.hcl#lint.go" = {
    file   = "atte.hcl"
    index  = 0
    inline = ""
    kind   = "attehcl:lint"
    label  = "go"
    name   = "go"
    script = "atte_lint.sh"
  }
  "//atte.hcl#test.build" = {
    file   = "atte.hcl"
    index  = 1
    inline = ""
    kind   = "attehcl:test"
    label  = "build"
    name   = "build"
    script = "atte_build.sh"
  }
  "//atte.hcl#test.go" = {
    file   = "atte.hcl"
    index  = 0
    inline = ""
    kind   = "attehcl:test"
    label  = "go"
    name   = "go"
    script = "atte_test.sh"
  }
}
