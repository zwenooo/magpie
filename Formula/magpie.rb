class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.4562-vb.0.0.1"
  license "MIT"

  on_macos do
    depends_on arch: :arm64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.4562-vb.0.0.1/magpie-cli-darwin-arm64"
    sha256 "891bbe88e655467b44dd4fa47d30bd5e92d355ed7a369e5b8351fdf522bd6ffd"
  end
  on_linux do
    depends_on arch: :x86_64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.4562-vb.0.0.1/magpie-cli-linux-amd64"
    sha256 "c7b43e7ff2acda57495e323ea07a3fa457273eccd23a8e0bb007d62cc7096727"
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
