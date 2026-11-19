class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.4501-vb.0.0.1"
  license "MIT"

  on_macos do
    depends_on arch: :arm64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.4501-vb.0.0.1/magpie-cli-darwin-arm64"
    sha256 "007290fc2a3a8e0e51d49362c077f9d6205130cf98db7ab3eed4f3d353b7970e"
  end
  on_linux do
    depends_on arch: :x86_64
    url "https://github.com/zwenooo/magpie/releases/download/va.0.1.4501-vb.0.0.1/magpie-cli-linux-amd64"
    sha256 "b939afc4127615124c70f80a65421aec6685d9438215f80c1cd7ca1316cfe853"
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
