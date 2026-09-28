/**
 * 应用版本号的唯一读取点。
 *
 * 值由两处 vite 配置在构建时注入（都读 apps/desktop/package.json，
 * 见各自的 vite.config.ts）。界面里**不要**再手写 "v0.1.0" 这种字符串——
 * 之前就是手写在标题栏和侧栏里的，发版时会漏改，界面就一直显示旧版本。
 */
export const APP_VERSION: string =
  (import.meta.env.VITE_APP_VERSION as string | undefined) ?? "0.0.0";

/** 带 v 前缀的展示形式（"v0.1.2"）。 */
export const APP_VERSION_TAG = `v${APP_VERSION}`;
