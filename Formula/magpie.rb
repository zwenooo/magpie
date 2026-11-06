class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.3140-vb.0.0.2"
  license "MIT"

  on_macos do
    depends_on arch: :arm64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3140-vb.0.0.2/magpie-cli-darwin-arm64"
    sha256 "9d61f1f6e9d7e422368cf3f105fae35ab0a0f3fddcab4b1a73eb4605b27711e2"
  end
  on_linux do
    depends_on arch: :x86_64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3140-vb.0.0.2/magpie-cli-linux-amd64"
    sha256 "c5f86e057fb90e62fa6788e1eec6ebafb07c8a1fecab59e922b6b09f8a964cac"
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
