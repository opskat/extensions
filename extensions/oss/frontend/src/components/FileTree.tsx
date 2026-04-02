import { useState, useCallback, useEffect } from "react";
import { cn } from "@opskat/ui";
import { ChevronRight, Folder, FolderOpen } from "lucide-react";
import { useOSSClient } from "../hooks/useOSSClient";

interface TreeNode {
  prefix: string;
  name: string;
  children?: TreeNode[];
  loaded: boolean;
  expanded: boolean;
}

interface FileTreeProps {
  /** Current bucket */
  bucket: string;
  /** Top-level common prefixes from the initial browse */
  rootPrefixes: string[];
  /** Currently active prefix in the browser */
  activePrefix: string;
  onNavigate: (prefix: string) => void;
}

function prefixToName(prefix: string): string {
  const parts = prefix.replace(/\/$/, "").split("/");
  return parts[parts.length - 1] + "/";
}

function TreeItem({
  node,
  depth,
  activePrefix,
  onNavigate,
  onToggle,
}: {
  node: TreeNode;
  depth: number;
  activePrefix: string;
  onNavigate: (prefix: string) => void;
  onToggle: (prefix: string) => void;
}) {
  const isActive = activePrefix === node.prefix;
  const FolderIcon = node.expanded ? FolderOpen : Folder;

  return (
    <>
      <div
        className={cn(
          "flex items-center gap-1 w-full text-sm py-1 px-1 rounded hover:bg-accent/60 transition-colors cursor-pointer",
          isActive && "bg-primary/10 text-primary font-medium",
        )}
        style={{ paddingLeft: `${depth * 16 + 4}px` }}
        onClick={() => onNavigate(node.prefix)}
      >
        <button
          onClick={(e) => {
            e.stopPropagation();
            onToggle(node.prefix);
          }}
          className="shrink-0 p-0.5 hover:bg-accent rounded"
        >
          <ChevronRight
            className={cn(
              "h-3 w-3 transition-transform",
              node.expanded && "rotate-90",
            )}
          />
        </button>
        <FolderIcon className="h-3.5 w-3.5 shrink-0 text-primary" />
        <span className="truncate">{node.name}</span>
      </div>
      {node.expanded &&
        node.children?.map((child) => (
          <TreeItem
            key={child.prefix}
            node={child}
            depth={depth + 1}
            activePrefix={activePrefix}
            onNavigate={onNavigate}
            onToggle={onToggle}
          />
        ))}
    </>
  );
}

export function FileTree({ bucket, rootPrefixes, activePrefix, onNavigate }: FileTreeProps) {
  const client = useOSSClient();
  const [nodes, setNodes] = useState<TreeNode[]>(() =>
    rootPrefixes.map((p) => ({
      prefix: p,
      name: prefixToName(p),
      loaded: false,
      expanded: false,
    })),
  );

  // Update root nodes when rootPrefixes changes
  // (keep expanded state for existing nodes)
  const updateRoots = useCallback(
    (newPrefixes: string[]) => {
      setNodes((prev) => {
        const existing = new Map(prev.map((n) => [n.prefix, n]));
        return newPrefixes.map(
          (p) =>
            existing.get(p) ?? {
              prefix: p,
              name: prefixToName(p),
              loaded: false,
              expanded: false,
            },
        );
      });
    },
    [],
  );

  useEffect(() => {
    updateRoots(rootPrefixes);
  }, [rootPrefixes, updateRoots]);

  // Lazy-load children when expanding
  const toggleNode = useCallback(
    async (prefix: string) => {
      const updateTree = (
        items: TreeNode[],
        target: string,
        updater: (n: TreeNode) => TreeNode,
      ): TreeNode[] =>
        items.map((n) => {
          if (n.prefix === target) return updater(n);
          if (n.children) return { ...n, children: updateTree(n.children, target, updater) };
          return n;
        });

      // Check current state to decide action
      let needsLoad = false;
      setNodes((prev) => {
        const node = findNode(prev, prefix);
        if (!node) return prev;
        if (node.expanded) {
          return updateTree(prev, prefix, (n) => ({ ...n, expanded: false }));
        }
        if (node.loaded) {
          return updateTree(prev, prefix, (n) => ({ ...n, expanded: true }));
        }
        needsLoad = true;
        return prev;
      });

      if (needsLoad) {
        try {
          const res = await client.browse({ bucket, prefix, delimiter: "/" });
          const children: TreeNode[] = (res.commonPrefixes || []).map((p) => ({
            prefix: p,
            name: prefixToName(p),
            loaded: false,
            expanded: false,
          }));
          setNodes((prev) =>
            updateTree(prev, prefix, (n) => ({
              ...n,
              children,
              loaded: true,
              expanded: true,
            })),
          );
        } catch {
          setNodes((prev) =>
            updateTree(prev, prefix, (n) => ({
              ...n,
              children: [],
              loaded: true,
              expanded: true,
            })),
          );
        }
      }
    },
    [client, bucket],
  );

  return (
    <div className="py-1">
      {/* Root (/) entry */}
      <button
        className={cn(
          "flex items-center gap-1 w-full text-left text-sm py-1 px-2 rounded hover:bg-accent/60",
          activePrefix === "" && "bg-primary/10 text-primary font-medium",
        )}
        onClick={() => onNavigate("")}
      >
        <Folder className="h-3.5 w-3.5 shrink-0 text-primary" />
        <span>/</span>
      </button>

      {nodes.map((node) => (
        <TreeItem
          key={node.prefix}
          node={node}
          depth={0}
          activePrefix={activePrefix}
          onNavigate={onNavigate}
          onToggle={toggleNode}
        />
      ))}
    </div>
  );
}

function findNode(items: TreeNode[], prefix: string): TreeNode | undefined {
  for (const n of items) {
    if (n.prefix === prefix) return n;
    if (n.children) {
      const found = findNode(n.children, prefix);
      if (found) return found;
    }
  }
  return undefined;
}
