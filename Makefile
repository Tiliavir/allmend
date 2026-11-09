.PHONY: build test lint check run tidy docker-build clean validate demo

build:
	CGO_ENABLED=0 go build -trimpath -o allmend ./cmd/allmend

test:
	go test -race ./...

lint:
	golangci-lint run ./...

# Everything CI runs, locally.
check: lint test validate

# Local development: plain HTTP, password login only.
# Set ALLMEND_ADMIN_PASSWORD_HASH first: echo 'your-password' | go run ./cmd/allmend hashpw
run:
	ALLMEND_SECURE_COOKIE=false ALLMEND_ADMIN_EMAIL=$${ALLMEND_ADMIN_EMAIL:-admin@example.org} go run ./cmd/allmend

tidy:
	go mod tidy

docker-build:
	docker build -t allmend .

clean:
	rm -f allmend

# Static demo site with made-up data (published to GitHub Pages by CI).
demo:
	rm -rf .cache/demo
	go run ./cmd/allmend demo .cache/demo
	@echo "preview: cd .cache/demo && python3 -m http.server"

# W3C validation of all rendered pages and the CSS (needs Java; vnu.jar from npm).
VNU := .cache/vnu.jar
$(VNU):
	mkdir -p .cache && cd .cache && npm pack vnu-jar@26.11.4 --silent && tar xzf vnu-jar-*.tgz && cp package/build/dist/vnu.jar vnu.jar
validate: $(VNU)
	rm -rf .cache/html && mkdir -p .cache/html
	ALLMEND_HTML_DUMP=$(CURDIR)/.cache/html go test -count=1 -run TestPagesRender ./internal/webui/
	rm -rf .cache/demo && go run ./cmd/allmend demo .cache/demo
	java -jar $(VNU) --also-check-css --also-check-svg .cache/html ui/static/*.css ui/static/*.svg
	java -jar $(VNU) --skip-non-html .cache/demo
