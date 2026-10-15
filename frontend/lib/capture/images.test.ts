import { describe, expect, test } from "vitest";
import { confidencePercent, fitSize, isImageFile, runLimited } from "./images";

describe("fitSize", () => {
  test("scales the long edge down to the limit", () => {
    expect(fitSize(4032, 3024)).toEqual({ width: 1600, height: 1200 });
    expect(fitSize(3024, 4032)).toEqual({ width: 1200, height: 1600 });
  });
  test("never upscales", () => {
    expect(fitSize(800, 600)).toEqual({ width: 800, height: 600 });
    expect(fitSize(1600, 1600)).toEqual({ width: 1600, height: 1600 });
  });
  test("handles degenerate input", () => {
    expect(fitSize(0, 100)).toEqual({ width: 0, height: 0 });
    expect(fitSize(10000, 1)).toEqual({ width: 1600, height: 1 });
  });
});

describe("isImageFile", () => {
  test("accepts image mime types and extension-only HEIC", () => {
    expect(isImageFile({ type: "image/jpeg", name: "a.jpg" })).toBe(true);
    expect(isImageFile({ type: "", name: "IMG_0001.HEIC" })).toBe(true);
    expect(isImageFile({ type: "application/pdf", name: "a.pdf" })).toBe(false);
    expect(isImageFile({ type: "", name: "notes.txt" })).toBe(false);
  });
});

describe("runLimited", () => {
  test("never exceeds the concurrency limit and runs every job", async () => {
    let active = 0;
    let peak = 0;
    const done: number[] = [];
    await runLimited([1, 2, 3, 4, 5, 6, 7], 2, async n => {
      active++;
      peak = Math.max(peak, active);
      await new Promise(r => setTimeout(r, 5));
      active--;
      done.push(n);
    });
    expect(peak).toBe(2);
    expect(done.sort()).toEqual([1, 2, 3, 4, 5, 6, 7]);
  });
  test("a failing job does not stop the others", async () => {
    const done: number[] = [];
    await runLimited([1, 2, 3], 2, async n => {
      if (n === 2) throw new Error("boom");
      done.push(n);
    });
    expect(done.sort()).toEqual([1, 3]);
  });
  test("empty input resolves", async () => {
    await runLimited([], 2, async () => {});
  });
});

describe("confidencePercent", () => {
  test("rounds and clamps", () => {
    expect(confidencePercent(0.876)).toBe(88);
    expect(confidencePercent(1.4)).toBe(100);
    expect(confidencePercent(-1)).toBe(0);
  });
});
