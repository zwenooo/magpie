class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.3140-vb.0.0.3"
  license "MIT"

  on_macos do
    depends_on arch: :arm64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3140-vb.0.0.3/magpie-cli-darwin-arm64"
    sha256 "6cbcb8db273afabd2ba90b2fcdacb53e75c83410bb28cbeeb7154bf09c188cc0"
  end
  on_linux do
    depends_on arch: :x86_64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3140-vb.0.0.3/magpie-cli-linux-amd64"
    sha256 "dcf95f8b98eb31f95f50c006930e1f846160ae1ebb1482328df4d6cfbb7e7cc4"
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
