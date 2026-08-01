.PHONY: fmt gen

fmt:
	treefmt

gen: export ATTE_CODEGEN=1
gen:
	go test ./detector/attego/... -run TestGraphvizSnapshot
	go test ./cmd
