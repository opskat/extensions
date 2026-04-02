// examples/echo/frontend/index.js

// Extension gets React from the host via window.__OPSKAT_EXT__
const { React } = window.__OPSKAT_EXT__;
const { useState } = React;
const h = React.createElement;

export function EchoPage({ assetId }) {
  const [message, setMessage] = useState("");
  const [result, setResult] = useState(null);
  const [loading, setLoading] = useState(false);
  const [events, setEvents] = useState([]);

  async function handleEcho() {
    setLoading(true);
    setResult(null);
    try {
      const res = await window.__OPSKAT_EXT__.api.callTool("echo", "echo", {
        message,
      });
      setResult(res);
    } catch (err) {
      setResult({ error: String(err) });
    }
    setLoading(false);
  }

  async function handleStream() {
    setLoading(true);
    setEvents([]);
    try {
      const res = await window.__OPSKAT_EXT__.api.executeAction(
        "echo",
        "echo_stream",
        { message },
        (e) => setEvents((prev) => [...prev, e]),
      );
      setResult(res);
    } catch (err) {
      setResult({ error: String(err) });
    }
    setLoading(false);
  }

  return h(
    "div",
    { style: { padding: "24px", maxWidth: "600px" } },
    h("h1", { style: { fontSize: "20px", fontWeight: "bold", marginBottom: "16px" } }, "Echo Extension"),
    h(
      "div",
      { style: { display: "flex", gap: "8px", marginBottom: "16px" } },
      h("input", {
        value: message,
        onChange: (e) => setMessage(e.target.value),
        placeholder: "Enter message",
        style: {
          flex: 1,
          padding: "6px 10px",
          border: "1px solid #ccc",
          borderRadius: "6px",
        },
      }),
      h(
        "button",
        {
          onClick: handleEcho,
          disabled: loading || !message,
          style: {
            padding: "6px 16px",
            background: "#3b82f6",
            color: "white",
            borderRadius: "6px",
            border: "none",
            cursor: loading ? "wait" : "pointer",
          },
        },
        "Echo Tool",
      ),
      h(
        "button",
        {
          onClick: handleStream,
          disabled: loading || !message,
          style: {
            padding: "6px 16px",
            background: "#8b5cf6",
            color: "white",
            borderRadius: "6px",
            border: "none",
            cursor: loading ? "wait" : "pointer",
          },
        },
        "Stream Action",
      ),
    ),
    result &&
      h(
        "pre",
        {
          style: {
            background: "#f3f4f6",
            padding: "12px",
            borderRadius: "6px",
            fontSize: "13px",
            overflow: "auto",
          },
        },
        JSON.stringify(result, null, 2),
      ),
    events.length > 0 &&
      h(
        "div",
        { style: { marginTop: "12px" } },
        h("h3", { style: { fontWeight: "600", marginBottom: "8px" } }, "Events:"),
        h(
          "div",
          {
            style: {
              background: "#f3f4f6",
              padding: "12px",
              borderRadius: "6px",
              fontSize: "13px",
              maxHeight: "200px",
              overflow: "auto",
            },
          },
          events.map((ev, i) =>
            h("div", { key: i }, `[${ev.eventType}] ${JSON.stringify(ev.data)}`),
          ),
        ),
      ),
  );
}
