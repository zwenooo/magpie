class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.3001-vb.0.0.4"
  license "MIT"

  on_macos do
    depends_on arch: :arm64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.4/magpie-cli-darwin-arm64"
    sha256 "c39f1daf0ecf55129a0c5c0182ab1d593363c40616f057ace9353e1d7168505f"
  end
  on_linux do
    depends_on arch: :x86_64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.4/magpie-cli-linux-amd64"
    sha256 "31ef451a8a2f30a8a99279dd39619bbd3f093dceb32a893124aede43a6eacad9"
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
