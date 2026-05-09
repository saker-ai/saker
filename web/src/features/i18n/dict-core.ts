export const dictCore = {
  // -- Nav --
  "nav.chats": { en: "Chats", zh: "对话" },
  "nav.skills": { en: "Skills", zh: "技能" },
  "nav.canvas": { en: "Canvas", zh: "画布" },
  "nav.settings": { en: "Settings", zh: "设置" },
  "nav.lightMode": { en: "Light mode", zh: "浅色模式" },
  "nav.darkMode": { en: "Dark mode", zh: "深色模式" },
  "nav.switchToLight": { en: "Switch to light mode", zh: "切换为浅色模式" },
  "nav.switchToDark": { en: "Switch to dark mode", zh: "切换为深色模式" },

  // -- ThreadPanel --
  "thread.new": { en: "+ New", zh: "+ 新建" },
  "thread.newChat": { en: "New chat", zh: "新建对话" },
  "thread.empty": { en: "No conversations yet", zh: "暂无对话" },
  "thread.deleteConfirm": { en: "Confirm", zh: "确认" },
  "thread.deleteCancel": { en: "Cancel", zh: "取消" },
  "thread.deleteTitle": { en: "Delete conversation", zh: "删除对话" },
  "thread.collapsePanel": { en: "Collapse panel", zh: "收起面板" },
  "thread.expandPanel": { en: "Expand panel", zh: "展开面板" },

  // -- ChatApp / EmptyState --
  "empty.subtitle": { en: "AI Agent", zh: "AI 智能体" },
  "empty.newChat": { en: "New Chat", zh: "新建对话" },
  "empty.newChatDesc": { en: "Start a new conversation thread", zh: "开始一个新的对话" },
  "empty.projectSummary": { en: "Project Summary", zh: "项目概览" },
  "empty.projectSummaryDesc": { en: "Get an overview of the current project", zh: "获取当前项目的概览" },
  "empty.taskPlan": { en: "Task Plan", zh: "任务计划" },
  "empty.taskPlanDesc": { en: "Identify tasks and create a plan", zh: "识别任务并创建计划" },
  "empty.waitingServer": { en: "Waiting for server connection at", zh: "正在等待服务器连接" },
  "empty.howCanIHelp": { en: "How can I help?", zh: "有什么可以帮您？" },
  "chat.selectOrCreate": { en: "Select or create a thread to start chatting", zh: "选择或创建一个对话开始聊天" },
  "chat.openChat": { en: "Open chat", zh: "打开对话" },
  "chat.closeChat": { en: "Close chat", zh: "关闭对话" },
  "chat.openChatList": { en: "Open chat list", zh: "打开对话列表" },

  // -- Composer --
  "composer.placeholder": { en: "Message Saker...", zh: "输入消息..." },
  "composer.send": { en: "Send message", zh: "发送消息" },
  "composer.stop": { en: "Stop generation", zh: "停止生成" },

  // -- StatusBar --
  "status.disconnected": { en: "Disconnected", zh: "已断开" },
  "status.thinking": { en: "Agent is thinking...", zh: "智能体思考中..." },
  "status.waiting": { en: "Waiting for approval", zh: "等待审批" },
  "status.error": { en: "Error occurred", zh: "发生错误" },
  "status.ready": { en: "Ready", zh: "就绪" },

  // -- MessageStream --
  "message.you": { en: "You", zh: "你" },
  "message.copy": { en: "Copy", zh: "复制" },
  "message.copied": { en: "Copied!", zh: "已复制！" },
  "message.copyMessage": { en: "Copy message", zh: "复制消息" },
  "message.showMore": { en: "Show more", zh: "展开更多" },
  "message.showLess": { en: "Show less", zh: "收起" },
  "message.running": { en: "Running...", zh: "运行中..." },
  "message.failed": { en: "Failed", zh: "失败" },
  "message.done": { en: "Done", zh: "完成" },
  "message.output": { en: "Output", zh: "输出" },
  "message.lines": { en: "lines", zh: "行" },
  "message.line": { en: "line", zh: "行" },
  "message.toolCall": { en: "call", zh: "次调用" },
  "message.toolCalls": { en: "calls", zh: "次调用" },
  "message.completed": { en: "completed", zh: "已完成" },
  "message.imageFailedToLoad": { en: "Image failed to load", zh: "图片加载失败" },
  "message.fullSizePreview": { en: "Full size preview", zh: "全尺寸预览" },
  "message.generatedImage": { en: "Generated image", zh: "生成的图片" },

  // -- ApprovalCard --
  "approval.requiresApproval": { en: "requires approval", zh: "需要审批" },
  "approval.allow": { en: "Allow", zh: "允许" },
  "approval.deny": { en: "Deny", zh: "拒绝" },
} as const;

export type DictCoreKey = keyof typeof dictCore;
