class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.3001-vb.0.0.2"
  license "MIT"

  on_macos do
    depends_on arch: :arm64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.2/magpie-cli-darwin-arm64"
    sha256 "7f4354477af96a682089f891657447ab0d3bbe7627c90d5116e7e9daa8f0d03f"
  end
  on_linux do
    depends_on arch: :x86_64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.2/magpie-cli-linux-amd64"
    sha256 "a0e19dc784113664b1851a520e947cc8d5c907f9ca2c2a288f852d046ce3d6dd"
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
