class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.2549-vb.0.0.2"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.2549-vb.0.0.2/magpie-cli-darwin-arm64"
      sha256 "ce4bae1608041de30262fdcd5e79fdc25061f38bcf179c4190fafb1829bf7c1c"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.2549-vb.0.0.2/magpie-cli-darwin-amd64"
      sha256 "3b4b4fdef871ad358a088d03b3dcceb07ca99ffa9262c880c7ad7ef1c489c9ac"
    end
  end
  on_linux do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.2549-vb.0.0.2/magpie-cli-linux-arm64"
      sha256 "1d4bd8f5d0b0a85d4429484717fd2749bdeebcbbcb5ea8d5fd7e9109a43fc850"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.2549-vb.0.0.2/magpie-cli-linux-amd64"
      sha256 "ec489fc0f56113bc1f5e63e439e16a47c2608ee60c1b9019303db041894151e7"
    end
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
