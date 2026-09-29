import postcss from "postcss";
import { describe, expect, it } from "vitest";
import { scopeToPage } from "./scope-css";

const scope = ":where(.ext-elasticsearch,.ext-elasticsearch *)";
const run = (css: string) => postcss([scopeToPage()]).process(css, { from: undefined }).css;

describe("scopeToPage", () => {
  it("limits every utility to the page root and its descendants, in every selector of a rule", () => {
    expect(run(".grid-cols-2{a:b}")).toBe(`.grid-cols-2${scope}{a:b}`);
    expect(run(".bg-a,.bg-b{a:b}")).toBe(`.bg-a${scope},.bg-b${scope}{a:b}`);
  });

  it("scopes rules nested in media, container and supports queries and layers", () => {
    expect(run("@layer utilities{@container (min-width:48rem){.x{a:b}}}")).toBe(
      `@layer utilities{@container (min-width:48rem){.x${scope}{a:b}}}`
    );
  });

  it("keeps a pseudo-element last", () => {
    expect(run(".p\\:x::placeholder{a:b}")).toBe(`.p\\:x${scope}::placeholder{a:b}`);
  });

  it("leaves keyframe steps and Tailwind's global property defaults alone", () => {
    const css = "@keyframes spin{to{a:b}}@layer properties{*,:before,::backdrop{--tw-x:0}}";
    expect(run(css)).toBe(css);
  });
});
