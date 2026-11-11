import type { Metadata } from "next";
import "./globals.css";
import { ThemeProvider } from "./_components/theme-provider";
import { TooltipProvider } from "./_components/ui/tooltip";

export const metadata: Metadata = {
  title: "see-agent 工作台",
  description: "私有部署的单智能体知识问答工作台",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="zh-CN" data-theme="verdant-habitat">
      <body><ThemeProvider><TooltipProvider>{children}</TooltipProvider></ThemeProvider></body>
    </html>
  );
}
