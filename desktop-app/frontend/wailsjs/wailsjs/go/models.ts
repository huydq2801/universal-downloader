export namespace downloader {
	
	export class AIFormatInfo {
	    title: string;
	    url: string;
	    quality: string;
	    ext: string;
	
	    static createFrom(source: any = {}) {
	        return new AIFormatInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.url = source["url"];
	        this.quality = source["quality"];
	        this.ext = source["ext"];
	    }
	}
	export class DownloadRequest {
	    url: string;
	    pageUrl: string;
	    title: string;
	    type: string;
	    quality: string;
	    format: string;
	    savePath: string;
	    headers: Record<string, string>;
	    cookies: string;
	    blobData: string;
	
	    static createFrom(source: any = {}) {
	        return new DownloadRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.pageUrl = source["pageUrl"];
	        this.title = source["title"];
	        this.type = source["type"];
	        this.quality = source["quality"];
	        this.format = source["format"];
	        this.savePath = source["savePath"];
	        this.headers = source["headers"];
	        this.cookies = source["cookies"];
	        this.blobData = source["blobData"];
	    }
	}
	export class ExtractResult {
	    title: string;
	    thumbnail: string;
	    formats: AIFormatInfo[];
	
	    static createFrom(source: any = {}) {
	        return new ExtractResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.thumbnail = source["thumbnail"];
	        this.formats = this.convertValues(source["formats"], AIFormatInfo);
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
	export class FormatInfo {
	    format_id: string;
	    format_note: string;
	    ext: string;
	    width: number;
	    height: number;
	    fps: number;
	    vcodec: string;
	    acodec: string;
	    filesize: number;
	    tbr: number;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new FormatInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.format_id = source["format_id"];
	        this.format_note = source["format_note"];
	        this.ext = source["ext"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.fps = source["fps"];
	        this.vcodec = source["vcodec"];
	        this.acodec = source["acodec"];
	        this.filesize = source["filesize"];
	        this.tbr = source["tbr"];
	        this.url = source["url"];
	    }
	}
	export class MediaInfo {
	    id: string;
	    title: string;
	    description: string;
	    duration: number;
	    thumbnail: string;
	    uploader: string;
	    formats: FormatInfo[];
	    webpage_url: string;
	
	    static createFrom(source: any = {}) {
	        return new MediaInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.description = source["description"];
	        this.duration = source["duration"];
	        this.thumbnail = source["thumbnail"];
	        this.uploader = source["uploader"];
	        this.formats = this.convertValues(source["formats"], FormatInfo);
	        this.webpage_url = source["webpage_url"];
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

export namespace queue {
	
	export class Item {
	    id: string;
	    request: downloader.DownloadRequest;
	    status: string;
	    progress: number;
	    speed: string;
	    eta: string;
	    error: string;
	    savePath: string;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Item(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.request = this.convertValues(source["request"], downloader.DownloadRequest);
	        this.status = source["status"];
	        this.progress = source["progress"];
	        this.speed = source["speed"];
	        this.eta = source["eta"];
	        this.error = source["error"];
	        this.savePath = source["savePath"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
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

export namespace storage {
	
	export class DownloadRecord {
	    id: string;
	    url: string;
	    pageUrl: string;
	    title: string;
	    type: string;
	    status: string;
	    quality: string;
	    format: string;
	    savePath: string;
	    fileSize: number;
	    progress: number;
	    speed: string;
	    eta: string;
	    error: string;
	    thumbnail: string;
	    duration: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	    // Go type: time
	    completedAt?: any;
	
	    static createFrom(source: any = {}) {
	        return new DownloadRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.url = source["url"];
	        this.pageUrl = source["pageUrl"];
	        this.title = source["title"];
	        this.type = source["type"];
	        this.status = source["status"];
	        this.quality = source["quality"];
	        this.format = source["format"];
	        this.savePath = source["savePath"];
	        this.fileSize = source["fileSize"];
	        this.progress = source["progress"];
	        this.speed = source["speed"];
	        this.eta = source["eta"];
	        this.error = source["error"];
	        this.thumbnail = source["thumbnail"];
	        this.duration = source["duration"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	        this.completedAt = this.convertValues(source["completedAt"], null);
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
	export class Settings {
	    saveDirectory: string;
	    defaultQuality: string;
	    defaultFormat: string;
	    maxConcurrent: number;
	    downloadThreads: number;
	    ytdlpPath: string;
	    ffmpegPath: string;
	    autoOpenFolder: boolean;
	    theme: string;
	    geminiAPIKey: string;
	    geminiModel: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.saveDirectory = source["saveDirectory"];
	        this.defaultQuality = source["defaultQuality"];
	        this.defaultFormat = source["defaultFormat"];
	        this.maxConcurrent = source["maxConcurrent"];
	        this.downloadThreads = source["downloadThreads"];
	        this.ytdlpPath = source["ytdlpPath"];
	        this.ffmpegPath = source["ffmpegPath"];
	        this.autoOpenFolder = source["autoOpenFolder"];
	        this.theme = source["theme"];
	        this.geminiAPIKey = source["geminiAPIKey"];
	        this.geminiModel = source["geminiModel"];
	    }
	}

}

