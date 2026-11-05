class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.3001-vb.0.0.3"
  license "MIT"

  on_macos do
    depends_on arch: :arm64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.3/magpie-cli-darwin-arm64"
    sha256 "bb33f8d23943020151afd0b75455683a21b464c7d73331c04529d3b1c2702c17"
  end
  on_linux do
    depends_on arch: :x86_64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.3/magpie-cli-linux-amd64"
    sha256 "cd3af93f8a3c8cbaf5abd64f5dc5bbd72944827713c69c3b0a2ae5f3e844e0a7"
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
