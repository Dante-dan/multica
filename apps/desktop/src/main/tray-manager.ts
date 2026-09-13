import { app, Menu, nativeImage, Tray } from "electron";

export interface TrayManagerOptions {
  iconPath: string;
  showWindow: () => void;
  requestQuit: () => void;
}

let tray: Tray | null = null;

export function buildTrayMenuTemplate(actions: {
  showWindow: () => void;
  requestQuit: () => void;
}): Electron.MenuItemConstructorOptions[] {
  return [
    { label: "Open Multica", click: actions.showWindow },
    { type: "separator" },
    { label: "Quit Multica", click: actions.requestQuit },
  ];
}

export function setupTray(options: TrayManagerOptions): void {
  if (tray) return;

  const image = nativeImage.createFromPath(options.iconPath).resize({
    width: 18,
    height: 18,
  });

  tray = new Tray(image);
  tray.setToolTip("Multica");
  tray.setContextMenu(
    Menu.buildFromTemplate(
      buildTrayMenuTemplate({
        showWindow: options.showWindow,
        requestQuit: options.requestQuit,
      }),
    ),
  );
  tray.on("click", options.showWindow);
  tray.on("double-click", options.showWindow);

  app.on("before-quit", () => {
    tray?.destroy();
    tray = null;
  });
}

export function resetTrayForTests(): void {
  tray?.destroy();
  tray = null;
}
