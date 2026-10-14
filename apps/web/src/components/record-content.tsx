import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { label } from "../lib/api";

export function safeReference(value: string): string | undefined {
  // Relative references have no reliable repository base; never invent one.
  return /^https?:\/\//i.test(value) ? value : undefined;
}
export function RecordContent({ value }: { value: unknown }) {
  if (value == null) return <span className="quiet">Not recorded</span>;
  if (Array.isArray(value))
    return (
      <ul className="structured-list">
        {value.map((item, index) => (
          // Original array position is stable within an immutable capture.
          // biome-ignore lint/suspicious/noArrayIndexKey: immutable source positions
          <li key={index}>
            <RecordContent value={item} />
          </li>
        ))}
      </ul>
    );
  if (typeof value === "object")
    return (
      <dl className="structured-fields">
        {Object.entries(value).map(([key, item]) => (
          <div key={key}>
            <dt>{label(key)}</dt>
            <dd>
              <RecordContent value={item} />
            </dd>
          </div>
        ))}
      </dl>
    );
  return (
    <div className="record-content">
      <Markdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        urlTransform={(url) => safeReference(url) ?? ""}
        components={{
          a: ({ href, children }) =>
            href ? (
              <a href={href} target="_blank" rel="noreferrer">
                {children}
              </a>
            ) : (
              <span>{children}</span>
            ),
          img: ({ alt }) => (
            <span className="quiet">[Image: {alt || "attachment"}]</span>
          ),
          h1: ({ children }) => <h3>{children}</h3>,
          h2: ({ children }) => <h3>{children}</h3>,
          table: ({ children }) => (
            <div className="table-scroll">
              <table>{children}</table>
            </div>
          ),
        }}
      >
        {String(value)}
      </Markdown>
    </div>
  );
}
