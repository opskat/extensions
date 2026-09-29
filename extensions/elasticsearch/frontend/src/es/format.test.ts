import { describe, expect, it } from "vitest";
import { formatBytes, formatCount } from "./format";

describe("formatBytes", () => {
  it("scales to the largest unit under 1024, one decimal past bytes", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(3.3 * 1024 ** 3)).toBe("3.3 GB");
  });

  it("shows a dash for an unknown size (a closed index)", () => {
    expect(formatBytes(null)).toBe("—");
  });
});

describe("formatCount", () => {
  it("abbreviates large counts in the page's language", () => {
    expect(formatCount(1_240_000, "en")).toBe("1.2M");
    expect(formatCount(12_431, "en")).toBe("12.4K");
    expect(formatCount(999, "en")).toBe("999");
    expect(formatCount(null, "en")).toBe("—");
  });
});
