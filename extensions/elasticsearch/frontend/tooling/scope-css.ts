import type { AtRule, Container, Plugin, Rule } from "postcss";

/** The class on the page's root element; the stylesheet applies inside it only. */
export const ROOT_CLASS = "ext-elasticsearch";

const scope = `:where(.${ROOT_CLASS},.${ROOT_CLASS} *)`;

function inAtRule(rule: Rule, match: (at: AtRule) => boolean): boolean {
  for (let p = rule.parent as Container | undefined; p; p = p.parent as Container | undefined) {
    if (p.type === "atrule" && match(p as AtRule)) return true;
  }
  return false;
}

/**
 * Confines the page's utilities to the page. The stylesheet stays loaded for the
 * rest of the session and its class names are the host's own, so an unscoped
 * `.grid-cols-2` here would outrank a host dialog's `sm:grid-cols-3` (same layer,
 * later in the document). Appending a zero-specificity `:where()` keeps each
 * rule's specificity and ordering and only limits what it matches.
 */
export function scopeToPage(): Plugin {
  return {
    postcssPlugin: "scope-to-page",
    Once(root) {
      root.walkRules((rule) => {
        // Keyframe steps and Tailwind's global --tw-* defaults are not element rules.
        if (inAtRule(rule, (at) => at.name.endsWith("keyframes") || (at.name === "layer" && at.params === "properties"))) {
          return;
        }
        rule.selectors = rule.selectors.map((sel) => {
          const pseudo = sel.indexOf("::");
          return pseudo < 0 ? sel + scope : sel.slice(0, pseudo) + scope + sel.slice(pseudo);
        });
      });
    },
  };
}
