export namespace main {
	
	export class TreeNode {
	    oid: string;
	    title: string;
	    children: TreeNode[];
	
	    static createFrom(source: any = {}) {
	        return new TreeNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.oid = source["oid"];
	        this.title = source["title"];
	        this.children = this.convertValues(source["children"], TreeNode);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CollectionTree {
	    id: string;
	    name: string;
	    nodes: TreeNode[];
	
	    static createFrom(source: any = {}) {
	        return new CollectionTree(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.nodes = this.convertValues(source["nodes"], TreeNode);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ConnectionSettings {
	    apiURL: string;
	    hasKey: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.apiURL = source["apiURL"];
	        this.hasKey = source["hasKey"];
	    }
	}
	export class StorageInfo {
	    portable: boolean;
	    dataDir: string;
	    dbPath: string;
	
	    static createFrom(source: any = {}) {
	        return new StorageInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.portable = source["portable"];
	        this.dataDir = source["dataDir"];
	        this.dbPath = source["dbPath"];
	    }
	}
	export class StorageStatus {
	    portable: boolean;
	    locked: boolean;
	    hasDB: boolean;
	    legacy: boolean;
	    dataDir: string;
	
	    static createFrom(source: any = {}) {
	        return new StorageStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.portable = source["portable"];
	        this.locked = source["locked"];
	        this.hasDB = source["hasDB"];
	        this.legacy = source["legacy"];
	        this.dataDir = source["dataDir"];
	    }
	}
	export class SyncResult {
	    total: number;
	    added: number;
	    updated: number;
	    deleted: number;
	    meta: number;
	
	    static createFrom(source: any = {}) {
	        return new SyncResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.added = source["added"];
	        this.updated = source["updated"];
	        this.deleted = source["deleted"];
	        this.meta = source["meta"];
	    }
	}
	export class SyncStats {
	    lastSync: string;
	    total: number;
	    added: number;
	    updated: number;
	    deleted: number;
	
	    static createFrom(source: any = {}) {
	        return new SyncStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.lastSync = source["lastSync"];
	        this.total = source["total"];
	        this.added = source["added"];
	        this.updated = source["updated"];
	        this.deleted = source["deleted"];
	    }
	}

}

export namespace storage {
	
	export class Document {
	    ID: number;
	    OutlineID: string;
	    CollectionID: string;
	    ParentID: string;
	    UrlID: string;
	    Url: string;
	    Title: string;
	    Text: string;
	    Position: number;
	    // Go type: time
	    UpdatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Document(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.OutlineID = source["OutlineID"];
	        this.CollectionID = source["CollectionID"];
	        this.ParentID = source["ParentID"];
	        this.UrlID = source["UrlID"];
	        this.Url = source["Url"];
	        this.Title = source["Title"];
	        this.Text = source["Text"];
	        this.Position = source["Position"];
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

