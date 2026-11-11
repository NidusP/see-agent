import Link from "next/link";

export default function NotFound() {
  return (
    <main className="not-found-page">
      <section className="not-found-card" aria-labelledby="not-found-title">
        <p className="not-found-code">404</p>
        <h1 id="not-found-title">页面未找到</h1>
        <p>链接可能已失效，或地址输入有误。你可以返回工作台继续查阅内部知识。</p>
        <Link className="not-found-action" href="/chat">返回工作台</Link>
      </section>
    </main>
  );
}
