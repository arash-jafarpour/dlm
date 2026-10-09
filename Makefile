.PHONY: build install completion run

build:
	@go build -o dlm
	@$(MAKE) --no-print-directory completion

install: build
	@go install .
	@echo "installed $$(go env GOBIN)/dlm"

# Install shell completion for the current $SHELL (bash, zsh, or fish) into the
# shell's standard completion directory so it loads automatically.
completion:
	@if [ ! -x ./dlm ]; then echo "completion: ./dlm not found; run 'make build' first" >&2; exit 1; fi
	@shell="$$(basename "$${SHELL:-bash}")"; \
	case "$$shell" in \
		bash) dir="$${XDG_DATA_HOME:-$$HOME/.local/share}/bash-completion/completions"; file="dlm";; \
		zsh)  dir="$$HOME/.zsh/completions"; file="_dlm";; \
		fish) dir="$$HOME/.config/fish/completions"; file="dlm.fish";; \
		*) echo "completion: unsupported shell '$$shell' (run 'dlm completion <shell>' manually)"; exit 0;; \
	esac; \
	mkdir -p "$$dir"; \
	./dlm completion "$$shell" > "$$dir/$$file"; \
	echo "completion installed: $$dir/$$file"

run: build
	@./dlm
