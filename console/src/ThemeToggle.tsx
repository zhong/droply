import { useEffect, useState } from "react";
import { Button } from "@cloudflare/kumo";
import { MoonIcon, SunIcon } from "@phosphor-icons/react";

export function ThemeToggle() {
  const [dark, setDark] = useState(
    () => matchMedia("(prefers-color-scheme: dark)").matches,
  );
  useEffect(() => {
    document.documentElement.dataset.mode = dark ? "dark" : "light";
  }, [dark]);
  return (
    <Button
      variant="ghost"
      icon={dark ? SunIcon : MoonIcon}
      aria-label={dark ? "切换浅色主题" : "切换深色主题"}
      onClick={() => setDark(!dark)}
    >
      {dark ? "浅色" : "深色"}
    </Button>
  );
}
