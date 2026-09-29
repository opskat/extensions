import { describe, expect, it } from "vitest";
import { pageRequest, pagination } from "./paging";

describe("pageRequest", () => {
  it("asks for a full page from page * size", () => {
    expect(pageRequest(0, 50, 10000)).toEqual({ from: 0, size: 50 });
    expect(pageRequest(3, 20, 10000)).toEqual({ from: 60, size: 20 });
  });

  it("never lets from + size pass the result window", () => {
    // 10000 is not a multiple of 30: the last reachable page is cut at the window.
    expect(pageRequest(333, 30, 10000)).toEqual({ from: 9990, size: 10 });
    // A window smaller than one page still gets a first page.
    expect(pageRequest(0, 50, 20)).toEqual({ from: 0, size: 20 });
  });
});

describe("pagination", () => {
  it("pages through every hit when they fit in the window", () => {
    const p = pagination({ page: 0, pageSize: 50, total: 120, maxResultWindow: 10000 });
    expect(p).toMatchObject({ pageCount: 3, reachablePageCount: 3, limited: false, canPrev: false, canNext: true });
    expect(p.rangeStart).toBe(1);
    expect(p.rangeEnd).toBe(50);
  });

  it("ends on the last, partial page", () => {
    const p = pagination({ page: 2, pageSize: 50, total: 120, maxResultWindow: 10000 });
    expect(p).toMatchObject({ canPrev: true, canNext: false, rangeStart: 101, rangeEnd: 120 });
  });

  it("makes pages past the result window unreachable and says so", () => {
    const total = 3_812_000;
    const last = pagination({ page: 199, pageSize: 50, total, maxResultWindow: 10000 });
    expect(last).toMatchObject({ pageCount: 76240, reachablePageCount: 200, limited: true, canNext: false });
    expect(last.rangeEnd).toBe(10000);
    expect(pagination({ page: 198, pageSize: 50, total, maxResultWindow: 10000 }).canNext).toBe(true);
  });

  it("follows the index's own max_result_window", () => {
    const p = pagination({ page: 0, pageSize: 100, total: 5000, maxResultWindow: 1000 });
    expect(p).toMatchObject({ reachablePageCount: 10, limited: true });
  });

  it("is not limited when the hits end exactly at the window", () => {
    const p = pagination({ page: 199, pageSize: 50, total: 10000, maxResultWindow: 10000 });
    expect(p).toMatchObject({ pageCount: 200, reachablePageCount: 200, limited: false, canNext: false });
  });

  it("has one empty page and no range when there are no hits", () => {
    const p = pagination({ page: 0, pageSize: 50, total: 0, maxResultWindow: 10000 });
    expect(p).toMatchObject({ canPrev: false, canNext: false, limited: false, rangeStart: 0, rangeEnd: 0 });
  });
});
