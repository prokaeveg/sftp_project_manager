export interface Connection {
    mode: 'ssh_alias' | 'manual';
    authMode?: string;
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
    deploymentPath: string
}

export interface ProjectConnection {
    id: string;
    name: string;
    rootPath: string;
    connection: Connection;
    mappings: Mapping[];
    excludes?: string[]
}

export interface Project {
    id: string;
    name: string;
    localRoot: string;
    connections: ProjectConnection[];
    defaultConnectionID?: string
    excludeDotDirectories?: boolean;
    searchExcludedDirectories?: string[];
    searchIncludedDirectories?: string[];
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
    searchExcluded?: boolean
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

export type TransferStatus = 'waiting' | 'running' | 'completed' | 'failed' | 'cancelled'

export interface QueueItem extends BatchItem {
    status: TransferStatus | 'skipped';
    transferredBytes: number;
    error: string
}

export interface QueueJob {
    id: string;
    projectName: string;
    direction: 'upload' | 'download';
    server: string;
    status: TransferStatus;
    items: QueueItem[];
    totalBytes: number;
    transferredBytes: number;
    processedItems: number;
    error: string
}

export interface QueueState {
    paused: boolean;
    jobs: QueueJob[]
}

export interface NavigationStatus {
    state: 'disconnected' | 'connecting' | 'connected';
    projectID: string;
    connectionID: string
}

export interface AuthPrompt {
    id: string;
    prompt: string;
    canRemember: boolean;
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

export interface FileContent {
    projectID: string; connectionID: string; server: string; name: string;
    relativePath: string; path: string; text: string; size: number;
    binary: boolean; truncated: boolean; encoding: string;
}
export interface FileComparison { local: FileContent; remote: FileContent; equal: boolean; comparable: boolean }
export interface FileSearchResult { files: FileEntry[]; limited: boolean }

interface API {
    ReadLocalFile(projectID: string, relative: string): Promise<FileContent>;
    ReadRemoteFile(projectID: string, connectionID: string, relative: string): Promise<FileContent>;
    CompareFile(projectID: string, connectionID: string, relative: string): Promise<FileComparison>;
    SearchProjectFiles(projectID: string, query: string): Promise<FileSearchResult>;
    SetDirectorySearchExcluded(projectID: string, relative: string, excluded: boolean): Promise<Status>;
    GetStatus(): Promise<Status>;

    SaveProjectName(name: string): Promise<Status>;

    SaveProfile(profile: Profile): Promise<Status>;

    SaveProject(project: Project): Promise<Status>;

    SelectProject(projectID: string, connectionID: string, mappingID: string): Promise<Status>;

    DiscoverSSH(configPath: string): Promise<SSHDiscovery>;

    EvaluateSSH(connection: Connection, trusted: boolean): Promise<SSHEffective>;

    SelectLocalDirectory(current: string): Promise<string>;

    ListProjectLocal(relative: string): Promise<FileEntry[]>;

    ListLocal(relative: string): Promise<FileEntry[]>;

    ListRemote(relative: string): Promise<FileEntry[]>;

    GetNavigationStatus(): Promise<NavigationStatus>;

    DisconnectRemote(): Promise<void>;

    PreviewTransfer(direction: string, relative: string): Promise<Preview>;

    ExecuteTransfer(token: string, overwrite: boolean): Promise<void>;

    PreviewBatchForServer(projectID: string, connectionID: string, mappingID: string, direction: string, relatives: string[]): Promise<BatchPreview>;
    PreviewBatch(direction: string, relatives: string[]): Promise<BatchPreview>;

    ExecuteBatch(token: string, existingPolicy: 'skip' | 'overwrite', includeEnvironment: boolean): Promise<BatchResult>;

    GetQueue(): Promise<QueueState>;

    EnqueueBatch(token: string, policy: 'skip' | 'overwrite', includeEnvironment: boolean): Promise<QueueState>;

    CancelQueueJob(id: string): Promise<void>;

    SetQueuePaused(paused: boolean): Promise<void>;

    ClearFinishedQueue(): Promise<void>;

    PreviewQueueRetry(id: string): Promise<BatchPreview>;

    CancelOperation(): Promise<void>;

    GetAuthPrompt?(): Promise<AuthPrompt | null>;

    RespondAuthPrompt?(id: string, answer: string, cancelled: boolean, remember: boolean): Promise<void>;

    ForgetSavedPassword?(projectID: string, connectionID: string): Promise<void>;
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
