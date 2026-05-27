import "./globals.css";
import Link from "next/link";
import type { ReactNode } from "react";

export const metadata = {
  title: "AiLab — Agent Platform",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <header className="top">
          <div>
            <Link href="/agents">
              <strong>AiLab</strong>
            </Link>
            <span className="muted" style={{ marginLeft: 8 }}>
              agent platform
            </span>
          </div>
          <div>
            <Link href="/login" className="muted">
              token
            </Link>
          </div>
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
