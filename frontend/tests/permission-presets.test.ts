import { describe, expect, test } from "bun:test";
import type { RuntimeSettings } from "../src/types";
import { permissionMode, withPermissionPreset } from "../src/lib/permission-presets";

const fallbackRuntimeSettings = () => ({ sandboxMode: "workspace-write", filesystemAccess: "workspace-write", approvalPolicy: "on-request", networkAccess: true, shellAccess: true, allowDestructiveOps: false, extraWritableRoots: ["C:/test-extra"], computerControl: { anyAppEnabled: false, chromeEnabled: false, alwaysAllowedApps: ["test-fixture"] } } as RuntimeSettings);

describe("permission presets", () => {
  test("no prompts alone is not full access", () => {
    const settings = { ...fallbackRuntimeSettings(), approvalPolicy: "never" };
    expect(permissionMode(settings)).toBe("custom");
  });
  test("full access sets scope, shell, network and approval consistently", () => {
    const settings = fallbackRuntimeSettings();
    settings.networkAccess = false;
    settings.shellAccess = false;
    settings.allowDestructiveOps = true;
    const result = withPermissionPreset(settings, "full-access");
    expect(permissionMode(result)).toBe("full-access");
    expect(result.networkAccess).toBe(true);
    expect(result.shellAccess).toBe(true);
    expect(result.allowDestructiveOps).toBe(false);
    expect(result.computerControl).toEqual(settings.computerControl);
    expect(result.extraWritableRoots).toEqual(settings.extraWritableRoots);
    expect(settings.approvalPolicy).toBe("on-request");
  });
  test("returning to request approval restores both writable boundaries", () => {
    const settings = withPermissionPreset(withPermissionPreset(fallbackRuntimeSettings(), "full-access"), "workspace");
    expect(permissionMode(settings)).toBe("workspace");
    expect(settings.sandboxMode).toBe("workspace-write");
    expect(settings.filesystemAccess).toBe("workspace-write");
    expect(settings.approvalPolicy).toBe("on-request");
    expect(permissionMode(withPermissionPreset(settings, "read-only"))).toBe("read-only");
  });
});
