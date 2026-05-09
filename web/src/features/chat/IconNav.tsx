import { useT, type TKey } from "@/features/i18n";
import { MessageSquare, Wrench, Palette, ListTodo, Settings, LogIn, AppWindow } from "lucide-react";

export type NavView = "chats" | "skills" | "tasks" | "settings" | "canvas" | "apps";

interface Props {
  active: NavView;
  onChange: (view: NavView) => void;
  visible?: boolean;
  showLoginBtn?: boolean;
  onLoginClick?: () => void;
}

const itemDefs: { view: NavView; labelKey: TKey; icon: React.ReactNode }[] = [
  {
    view: "chats",
    labelKey: "nav.chats",
    icon: <MessageSquare size={20} strokeWidth={1.75} />,
  },
  {
    view: "skills",
    labelKey: "nav.skills",
    icon: <Wrench size={20} strokeWidth={1.75} />,
  },
  {
    view: "canvas",
    labelKey: "nav.canvas",
    icon: <Palette size={20} strokeWidth={1.75} />,
  },
  {
    view: "apps",
    labelKey: "nav.apps",
    icon: <AppWindow size={20} strokeWidth={1.75} />,
  },
  {
    view: "tasks",
    labelKey: "nav.tasks",
    icon: <ListTodo size={20} strokeWidth={1.75} />,
  },
  {
    view: "settings",
    labelKey: "nav.settings",
    icon: <Settings size={20} strokeWidth={1.75} />,
  },
];

export function IconNav({ active, onChange, visible, showLoginBtn, onLoginClick }: Props) {
  const { t } = useT();

  return (
    <nav className={`icon-nav${visible ? " icon-nav-visible" : ""}`} aria-label="Main navigation">
      <div className="icon-nav-items">
        {itemDefs.map((item) => (
          <button
            key={item.view}
            className={`icon-nav-btn ${active === item.view ? "active" : ""}`}
            onClick={() => onChange(item.view)}
            aria-label={t(item.labelKey)}
            aria-current={active === item.view ? "page" : undefined}
          >
            {item.icon}
            <span className="icon-nav-label">{t(item.labelKey)}</span>
            <span className="nav-tooltip" aria-hidden="true">{t(item.labelKey)}</span>
          </button>
        ))}
      </div>
      {showLoginBtn && onLoginClick && (
        <div className="icon-nav-footer">
          <button
            className="icon-nav-btn icon-nav-login"
            onClick={onLoginClick}
            aria-label={t("nav.login")}
          >
            <LogIn size={20} strokeWidth={1.75} />
            <span className="icon-nav-label">{t("nav.login")}</span>
            <span className="nav-tooltip" aria-hidden="true">{t("nav.login")}</span>
          </button>
        </div>
      )}
    </nav>
  );
}
