target = {
  "//atte.hcl#codegen.fmt" = {
    file   = "atte.hcl"
    id     = "attehcl:codegen:atte.hcl:fmt"
    index  = 0
    inline = ""
    kind   = "attehcl:codegen"
    label  = "fmt"
    name   = "fmt"
    script = "atte_fmt.sh"
  }
  "//atte.hcl#codegen.go" = {
    file   = "atte.hcl"
    id     = "attehcl:codegen:atte.hcl:go"
    index  = 1
    inline = ""
    kind   = "attehcl:codegen"
    label  = "go"
    name   = "go"
    script = "atte_codegen.sh"
  }
  "//atte.hcl#codegen.rendered" = {
    file   = "atte.hcl"
    id     = "attehcl:codegen:atte.hcl:rendered"
    index  = 2
    inline = ""
    kind   = "attehcl:codegen"
    label  = "rendered"
    name   = "rendered"
    script = "atte_codegen_rendered.sh"
  }
  "//atte.hcl#lint.go" = {
    file   = "atte.hcl"
    id     = "attehcl:lint:atte.hcl:go"
    index  = 0
    inline = ""
    kind   = "attehcl:lint"
    label  = "go"
    name   = "go"
    script = "atte_lint.sh"
  }
  "//atte.hcl#test.build" = {
    file   = "atte.hcl"
    id     = "attehcl:test:atte.hcl:build"
    index  = 1
    inline = ""
    kind   = "attehcl:test"
    label  = "build"
    name   = "build"
    script = "atte_build.sh"
  }
  "//atte.hcl#test.go" = {
    file   = "atte.hcl"
    id     = "attehcl:test:atte.hcl:go"
    index  = 0
    inline = ""
    kind   = "attehcl:test"
    label  = "go"
    name   = "go"
    script = "atte_test.sh"
  }
}
settings = {}
