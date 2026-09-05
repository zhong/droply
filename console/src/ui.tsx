import type { ReactNode } from "react";
import { Banner, Empty, Link, Loader, Table } from "@cloudflare/kumo";

export function Loading({ text = "正在加载…" }: { text?: string }) {
  return (
    <div role="status" className="flex items-center gap-3 p-6 text-kumo-subtle">
      <Loader aria-label={text} />
      {text}
    </div>
  );
}
export function ErrorNotice({ message }: { message: string }) {
  return <Banner variant="error" role="alert" title={message} />;
}
export function Panel({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="panel">
      <h2 className="panel-heading">{title}</h2>
      {children}
    </section>
  );
}
export function DataTable({
  headers,
  rows,
  label,
}: {
  headers: string[];
  rows: ReactNode[][];
  label: string;
}) {
  if (!rows.length)
    return (
      <Empty
        size="sm"
        title="暂无数据"
        description={`${label}暂时没有记录。`}
      />
    );
  return (
    <div
      className="table-scroll"
      data-wide={headers.length > 3}
      role="region"
      aria-label={label}
      tabIndex={0}
    >
      <Table aria-label={label}>
        <Table.Header>
          <Table.Row>
            {headers.map((header) => (
              <Table.Head key={header}>{header}</Table.Head>
            ))}
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {rows.map((cells, row) => (
            <Table.Row key={row}>
              {cells.map((cell, col) => (
                <Table.Cell key={col}>{cell}</Table.Cell>
              ))}
            </Table.Row>
          ))}
        </Table.Body>
      </Table>
    </div>
  );
}
export function SiteLink({
  label,
  domain,
  children,
}: {
  label?: string;
  domain: string;
  children: ReactNode;
}) {
  if (!label) return <>—</>;
  return (
    <Link
      href={`https://${label}.${domain}`}
      target="_blank"
      rel="noopener noreferrer"
    >
      {children}
      <Link.ExternalIcon />
    </Link>
  );
}
export const dateTime = (value: string) =>
  new Date(value).toLocaleString("zh-CN");
