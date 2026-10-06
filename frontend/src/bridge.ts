export interface Connection {
    mode: 'ssh_alias' | 'manual';
    alias: string;
    configPath: string;
    host: string;
    user: string;
    port: number;
    identityFile: string;
    knownHostsFile: string;
}

export interface Profile {
    name: string;
    localRoot: string;
    remoteRoot: string;
    connection: Connection;
    excludes?: string[];
    excludeBase?: string
}

export interface Mapping {
    id: string;
    localPath: string;
    remotePath: string
}

export interface ProjectConnection {
    id: string;
    name: string;
    connection: Connection;
    mappings: Mapping[];
    excludes?: string[]
}

export interface Project {
    id: string;
    name: string;
    localRoot: string;
    connections: ProjectConnection[]
}

export interface SSHDiscovery {
    configPath: string;
    aliases: { name: string; source: string; line: number; conditional: boolean }[];
    warnings: string[]
}

export interface SSHEffective {
    hostname: string;
    user: string;
    port: number;
    identityFiles: string[];
    proxyJump: string;
    hasProxyCommand: boolean;
    configPath: string;
    alias: string
}

export interface FileEntry {
    name: string;
    relativePath: string;
    size: number;
    isDir: boolean;
    isLink: boolean;
    excluded: boolean
}

export interface Preview {
    token: string;
    direction: 'upload' | 'download';
    server: string;
    source: string;
    destination: string
}

export interface BatchItem {
    relativePath: string;
    source: string;
    destination: string;
    kind: 'file' | 'directory';
    size: number;
    exists: boolean;
    skippedReason: string
}

export interface BatchPreview {
    token: string;
    direction: 'upload' | 'download';
    server: string;
    items: BatchItem[];
    totalBytes: number;
    fileCount: number
}

export interface BatchResult {
    items: {
        relativePath: string;
        kind: 'file' | 'directory';
        status: 'completed' | 'skipped' | 'failed' | 'cancelled';
        error: string
    }[]
}

export interface Status {
    webviewVersion: string;
    webviewMessage: string;
    platform: string;
    dataDir: string;
    webviewDataDir: string;
    sshPath: string;
    sshMessage: string;
    settings: {
        schemaVersion: number;
        projectName: string;
        profile?: Profile;
        projects: Project[];
        activeProjectID: string;
        activeConnectionID: string;
        activeMappingID: string
    }
}

interface API {
    GetStatus(): Promise<Status>;

    SaveProjectName(name: string): Promise<Status>;

    SaveProfile(profile: Profile): Promise<Status>;

    SaveProject(project: Project): Promise<Status>;

    SelectProject(projectID: string, connectionID: string, mappingID: string): Promise<Status>;

    DiscoverSSH(configPath: string): Promise<SSHDiscovery>;

    EvaluateSSH(connection: Connection, trusted: boolean): Promise<SSHEffective>;

    SelectLocalDirectory(current: string): Promise<string>;

    ListLocal(relative: string): Promise<FileEntry[]>;

    ListRemote(relative: string): Promise<FileEntry[]>;

    PreviewTransfer(direction: string, relative: string): Promise<Preview>;

    ExecuteTransfer(token: string, overwrite: boolean): Promise<void>;

    PreviewBatch(direction: string, relatives: string[]): Promise<BatchPreview>;

    ExecuteBatch(token: string, existingPolicy: 'skip' | 'overwrite', includeEnvironment: boolean): Promise<BatchResult>;

    CancelOperation(): Promise<void>;
}

declare global {
    interface Window {
        go?: { main: { App: API } }
    }
}

export function backend() {
    const api = window.go?.main.App
    if (!api) throw new Error('Откройте приложение через Wails: Go backend недоступен в отдельном браузере.')
    return api
}
