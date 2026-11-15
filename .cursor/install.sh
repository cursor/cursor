#!/usr/bin/env bash
# Install the toolchains and dependencies for the sibling quickstart-resources
# checkout. Idempotent: a second run skips anything already at the pinned version.
set -euo pipefail

NODE_VERSION=24.22.3
GO_VERSION=1.27.4
RUST_VERSION=1.100.0
RUBY_VERSION=3.4.7

NODE_PREFIX=/usr/local/lib/nodejs
GO_PREFIX=/usr/local/go

export PATH="${NODE_PREFIX}/bin:${GO_PREFIX}/bin:/usr/local/bin:${PATH}"

cursor_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
quickstart="${cursor_root}/../quickstart-resources"
if [[ ! -f "${quickstart}/Makefile" ]]; then
  echo "quickstart-resources checkout not found at ${quickstart}" >&2
  exit 1
fi

apt_packages() {
  sudo DEBIAN_FRONTEND=noninteractive apt-get update
  sudo DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    build-essential ca-certificates curl xz-utils pkg-config \
    libssl-dev libreadline-dev zlib1g-dev libyaml-dev libffi-dev autoconf bison
}

install_node() {
  if [[ -x "${NODE_PREFIX}/bin/node" ]] && [[ "$("${NODE_PREFIX}/bin/node" -v)" == "v${NODE_VERSION}" ]]; then
    echo "node ${NODE_VERSION} already installed"
    return
  fi
  local tarball
  tarball="$(mktemp)"
  curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" -o "${tarball}"
  sudo mkdir -p "${NODE_PREFIX}"
  sudo tar -xJf "${tarball}" -C "${NODE_PREFIX}" --strip-components=1 --no-same-owner
  rm -f "${tarball}"
}

install_go() {
  if [[ -x "${GO_PREFIX}/bin/go" ]] && [[ "$("${GO_PREFIX}/bin/go" version)" == "go version go${GO_VERSION} linux/amd64" ]]; then
    echo "go ${GO_VERSION} already installed"
    return
  fi
  local tarball
  tarball="$(mktemp)"
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o "${tarball}"
  sudo rm -rf "${GO_PREFIX}"
  sudo tar -C /usr/local -xzf "${tarball}"
  rm -f "${tarball}"
}

install_rust() {
  if rustc --version 2>/dev/null | grep -q "^rustc ${RUST_VERSION} "; then
    echo "rust ${RUST_VERSION} already installed"
    return
  fi
  if ! command -v rustup >/dev/null 2>&1; then
    curl --proto '=https' --tlsv1.2 -fsSL https://sh.rustup.rs | sh -s -- -y \
      --default-toolchain "${RUST_VERSION}" --profile minimal --component rustfmt,clippy
    # The rustup installer only edits rc files. Put the proxies on the default PATH.
    sudo ln -sfn "${HOME}/.cargo/bin/rustup" /usr/local/bin/rustup
    sudo ln -sfn "${HOME}/.cargo/bin/cargo" /usr/local/bin/cargo
    sudo ln -sfn "${HOME}/.cargo/bin/rustc" /usr/local/bin/rustc
    sudo ln -sfn "${HOME}/.cargo/bin/rustfmt" /usr/local/bin/rustfmt
    sudo ln -sfn "${HOME}/.cargo/bin/cargo-fmt" /usr/local/bin/cargo-fmt
    sudo ln -sfn "${HOME}/.cargo/bin/cargo-clippy" /usr/local/bin/cargo-clippy
    # shellcheck disable=SC1091
    source "${HOME}/.cargo/env"
    return
  fi
  rustup toolchain install "${RUST_VERSION}" --profile minimal --component rustfmt,clippy
  rustup default "${RUST_VERSION}"
}

install_ruby() {
  if [[ -x /usr/local/bin/ruby ]] && /usr/local/bin/ruby -v | grep -q "^ruby ${RUBY_VERSION}"; then
    echo "ruby ${RUBY_VERSION} already installed"
    return
  fi
  local src
  src="$(mktemp -d)"
  curl -fsSL "https://cache.ruby-lang.org/pub/ruby/3.4/ruby-${RUBY_VERSION}.tar.gz" -o "${src}/ruby.tar.gz"
  tar -xzf "${src}/ruby.tar.gz" -C "${src}"
  (
    cd "${src}/ruby-${RUBY_VERSION}"
    ./configure --prefix=/usr/local --disable-install-doc --disable-yjit
    make -j"$(nproc)"
    sudo make install
  )
  rm -rf "${src}"
}

install_uv() {
  if [[ -x /usr/local/bin/uv ]]; then
    echo "uv already installed ($(/usr/local/bin/uv --version))"
    return
  fi
  curl -LsSf https://astral.sh/uv/install.sh | env UV_INSTALL_DIR="${HOME}/.local/bin" sh
  sudo ln -sfn "${HOME}/.local/bin/uv" /usr/local/bin/uv
  sudo ln -sfn "${HOME}/.local/bin/uvx" /usr/local/bin/uvx
}

# Login shells source /etc/profile.d, then this image's ~/.bashrc (which
# prepends nvm and cargo). The bashrc line is last so these bins win.
configure_path() {
  local line='export PATH="/usr/local/lib/nodejs/bin:/usr/local/go/bin:/usr/local/bin:$PATH"'
  echo "${line}" | sudo tee /etc/profile.d/zz-dev-toolchains.sh >/dev/null
  local marker="# cursor-cloud-toolchains"
  if ! grep -qF "${marker}" "${HOME}/.bashrc"; then
    printf '\n%s\n%s\n' "${marker}" "${line}" >> "${HOME}/.bashrc"
  fi
}

install_quickstart_deps() {
  local dir
  for dir in weather-server-typescript mcp-client-typescript tests/helpers; do
    npm --prefix "${quickstart}/${dir}" ci
    npm --prefix "${quickstart}/${dir}" run build
  done

  for dir in weather-server-python mcp-client-python; do
    uv sync --frozen --directory "${quickstart}/${dir}"
  done

  (cd "${quickstart}/weather-server-go" && go mod download)
  (cd "${quickstart}/mcp-client-go" && go mod download)

  (cd "${quickstart}/weather-server-rust" && cargo fetch && cargo build --release)
  (cd "${quickstart}/mcp-client-rust" && cargo fetch && cargo build --release)

  bundle config set --global path "${HOME}/.local/share/quickstart/bundle"
  (cd "${quickstart}/weather-server-ruby" && bundle install)
  (cd "${quickstart}/mcp-client-ruby" && bundle install)
}

apt_packages
install_node
install_go
install_rust
install_ruby
install_uv
configure_path
install_quickstart_deps

echo "toolchains:"
node -v
go version
rustc --version
ruby -v
uv --version
