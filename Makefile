.PHONY: fmt gen

fmt:
	treefmt

gen:
	UPDATE_GO_LIST_DOT=1 go test ./detector/attego/... -run TestGraphvizSnapshot
