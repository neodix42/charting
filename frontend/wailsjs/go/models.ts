export namespace domain {
	
	export class Candle {
	    time: number;
	    open: number;
	    high: number;
	    low: number;
	    close: number;
	    volume: number;
	    closed: boolean;
	    quality: string;
	    sourceRevision: number;
	
	    static createFrom(source: any = {}) {
	        return new Candle(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.open = source["open"];
	        this.high = source["high"];
	        this.low = source["low"];
	        this.close = source["close"];
	        this.volume = source["volume"];
	        this.closed = source["closed"];
	        this.quality = source["quality"];
	        this.sourceRevision = source["sourceRevision"];
	    }
	}
	export class View {
	    mode: string;
	    fromSec?: number;
	    toSec?: number;
	    barSpacing: number;
	    rightOffset: number;
	
	    static createFrom(source: any = {}) {
	        return new View(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.fromSec = source["fromSec"];
	        this.toSec = source["toSec"];
	        this.barSpacing = source["barSpacing"];
	        this.rightOffset = source["rightOffset"];
	    }
	}
	export class PriceRange {
	    min: number;
	    max: number;
	
	    static createFrom(source: any = {}) {
	        return new PriceRange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.min = source["min"];
	        this.max = source["max"];
	    }
	}
	export class PriceScale {
	    mode: string;
	    autoScale: boolean;
	    manualRange?: PriceRange;
	
	    static createFrom(source: any = {}) {
	        return new PriceScale(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.autoScale = source["autoScale"];
	        this.manualRange = this.convertValues(source["manualRange"], PriceRange);
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
	export class Chart {
	    id: string;
	    exchange: string;
	    market: string;
	    instrument: string;
	    timeframe: string;
	    chartType: string;
	    showVolume: boolean;
	    priceScale: PriceScale;
	    view: View;
	
	    static createFrom(source: any = {}) {
	        return new Chart(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.exchange = source["exchange"];
	        this.market = source["market"];
	        this.instrument = source["instrument"];
	        this.timeframe = source["timeframe"];
	        this.chartType = source["chartType"];
	        this.showVolume = source["showVolume"];
	        this.priceScale = this.convertValues(source["priceScale"], PriceScale);
	        this.view = this.convertValues(source["view"], View);
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
	export class FeedKey {
	    exchange: string;
	    market: string;
	    instrument: string;
	    timeframe: string;
	
	    static createFrom(source: any = {}) {
	        return new FeedKey(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exchange = source["exchange"];
	        this.market = source["market"];
	        this.instrument = source["instrument"];
	        this.timeframe = source["timeframe"];
	    }
	}
	export class Grid {
	    rows: number;
	    columns: number;
	    rowFractions: number[];
	    columnFractions: number[];
	
	    static createFrom(source: any = {}) {
	        return new Grid(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rows = source["rows"];
	        this.columns = source["columns"];
	        this.rowFractions = source["rowFractions"];
	        this.columnFractions = source["columnFractions"];
	    }
	}
	export class Health {
	    state: string;
	    reason: string;
	    lastMessageAt: number;
	    lastTradeAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Health(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.reason = source["reason"];
	        this.lastMessageAt = source["lastMessageAt"];
	        this.lastTradeAt = source["lastTradeAt"];
	    }
	}
	export class Instrument {
	    nativeId: string;
	    baseAsset: string;
	    quoteAsset: string;
	    priceIncrement: string;
	    sizeIncrement: string;
	    availability: string;
	    supportedIntervals: string[];
	    providerMetadata: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new Instrument(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nativeId = source["nativeId"];
	        this.baseAsset = source["baseAsset"];
	        this.quoteAsset = source["quoteAsset"];
	        this.priceIncrement = source["priceIncrement"];
	        this.sizeIncrement = source["sizeIncrement"];
	        this.availability = source["availability"];
	        this.supportedIntervals = source["supportedIntervals"];
	        this.providerMetadata = source["providerMetadata"];
	    }
	}
	
	
	export class Tab {
	    id: string;
	    name: string;
	    grid: Grid;
	    charts: Chart[];
	
	    static createFrom(source: any = {}) {
	        return new Tab(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.grid = this.convertValues(source["grid"], Grid);
	        this.charts = this.convertValues(source["charts"], Chart);
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
	
	export class Window {
	    width: number;
	    height: number;
	    maximized: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Window(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.width = source["width"];
	        this.height = source["height"];
	        this.maximized = source["maximized"];
	    }
	}
	export class Workspace {
	    schemaVersion: number;
	    app: string;
	    id: string;
	    theme: string;
	    displayTimezone: string;
	    activeTabId: string;
	    window: Window;
	    tabs: Tab[];
	
	    static createFrom(source: any = {}) {
	        return new Workspace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schemaVersion = source["schemaVersion"];
	        this.app = source["app"];
	        this.id = source["id"];
	        this.theme = source["theme"];
	        this.displayTimezone = source["displayTimezone"];
	        this.activeTabId = source["activeTabId"];
	        this.window = this.convertValues(source["window"], Window);
	        this.tabs = this.convertValues(source["tabs"], Tab);
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

export namespace market {
	
	export class History {
	    requestId: string;
	    feedKey: domain.FeedKey;
	    bars: domain.Candle[];
	    moreHistory: boolean;
	
	    static createFrom(source: any = {}) {
	        return new History(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requestId = source["requestId"];
	        this.feedKey = this.convertValues(source["feedKey"], domain.FeedKey);
	        this.bars = this.convertValues(source["bars"], domain.Candle);
	        this.moreHistory = source["moreHistory"];
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
	export class Snapshot {
	    leaseId: string;
	    revision: number;
	    bars: domain.Candle[];
	    health: domain.Health;
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.leaseId = source["leaseId"];
	        this.revision = source["revision"];
	        this.bars = this.convertValues(source["bars"], domain.Candle);
	        this.health = this.convertValues(source["health"], domain.Health);
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
	
	export class Ack {
	    revision: number;
	    acceptedEditSequence: number;
	
	    static createFrom(source: any = {}) {
	        return new Ack(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.revision = source["revision"];
	        this.acceptedEditSequence = source["acceptedEditSequence"];
	    }
	}
	export class Backup {
	    id: number;
	    revision: number;
	    createdAt: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new Backup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.revision = source["revision"];
	        this.createdAt = source["createdAt"];
	        this.reason = source["reason"];
	    }
	}
	export class Loaded {
	    workspace: domain.Workspace;
	    revision: number;
	    recovery: string;
	
	    static createFrom(source: any = {}) {
	        return new Loaded(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspace = this.convertValues(source["workspace"], domain.Workspace);
	        this.revision = source["revision"];
	        this.recovery = source["recovery"];
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

