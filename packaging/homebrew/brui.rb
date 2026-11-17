# Homebrew formula for brui. Publish it in a tap repository
# (github.com/lupohirp/homebrew-tap, file Formula/brui.rb), then:
#
#   brew install lupohirp/tap/brui
#
# On each release update `url` and `sha256`:
#   curl -sL https://github.com/lupohirp/brui/archive/refs/tags/vX.Y.Z.tar.gz | shasum -a 256
#
# It builds from source, so the binary is not quarantined by Gatekeeper and
# needs no signing. No browser dependency: on macOS the OAuth2 login uses a
# native WebKit window.
class Brui < Formula
  desc "Terminal UI for Bruno API collections"
  homepage "https://github.com/lupohirp/brui"
  url "https://github.com/lupohirp/brui/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "REPLACE_WITH_THE_TARBALL_SHA256"
  license "MIT"
  head "https://github.com/lupohirp/brui.git", branch: "main"

  depends_on "go" => :build

  def install
    ENV["CGO_ENABLED"] = "0"
    ldflags = "-s -w -X main.version=#{version}"
    system "go", "build", *std_go_args(ldflags:), "./cmd/brui"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/brui --version")
  end
end
