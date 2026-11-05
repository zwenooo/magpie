class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.3001-vb.0.0.1"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.1/magpie-cli-darwin-arm64"
      sha256 "6312879405e908c0d0a81cb6411b741bac03ce6a7d47681b517640a04628e639"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.1/magpie-cli-darwin-amd64"
      sha256 "45034c4badbfee43b98957008f51a67a367eda11304630d702f48f97c9758825"
    end
  end
  on_linux do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.1/magpie-cli-linux-arm64"
      sha256 "a05999fd14319b8637cdc4c398e31a9b73bf4eeeee25346a450c01a35f3de0c4"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.3001-vb.0.0.1/magpie-cli-linux-amd64"
      sha256 "d4103098ad748db0a594f3d58345c45d81931139c4339d40d1cd083886a2409b"
    end
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
