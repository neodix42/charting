export namespace candlestorage {
	
	export class Info {
	    enabled: boolean;
	    directory: string;
	    sizeBytes: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.directory = source["directory"];
	        this.sizeBytes = source["sizeBytes"];
	        this.error = source["error"];
	    }
	}

}

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
	export class DrawingPoint {
	    time: number;
	    price: number;
	
	    static createFrom(source: any = {}) {
	        return new DrawingPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.price = source["price"];
	    }
	}
	export class Drawing {
	    id: string;
	    type: string;
	    exchange: string;
	    market: string;
	    instrument: string;
	    points: DrawingPoint[];
	
	    static createFrom(source: any = {}) {
	        return new Drawing(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.exchange = source["exchange"];
	        this.market = source["market"];
	        this.instrument = source["instrument"];
	        this.points = this.convertValues(source["points"], DrawingPoint);
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
	export class Indicator {
	    id: string;
	    type: string;
	    parameters: Record<string, number>;
	    color?: string;
	    lineWidth?: number;
	    paneStretchFactor?: number;
	
	    static createFrom(source: any = {}) {
	        return new Indicator(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.parameters = source["parameters"];
	        this.color = source["color"];
	        this.lineWidth = source["lineWidth"];
	        this.paneStretchFactor = source["paneStretchFactor"];
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
	    indicators?: Indicator[];
	    drawings?: Drawing[];
	
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
	        this.indicators = this.convertValues(source["indicators"], Indicator);
	        this.drawings = this.convertValues(source["drawings"], Drawing);
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
	export class ChartRightGap {
	    unit: string;
	    value: number;
	
	    static createFrom(source: any = {}) {
	        return new ChartRightGap(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.unit = source["unit"];
	        this.value = source["value"];
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
	    market: string;
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
	        this.market = source["market"];
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
	    chartRightGap?: ChartRightGap;
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
	        this.chartRightGap = this.convertValues(source["chartRightGap"], ChartRightGap);
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

export namespace screener {
	
	export class Instrument {
	    id: string;
	    name: string;
	    earliestDate: string;
	
	    static createFrom(source: any = {}) {
	        return new Instrument(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.earliestDate = source["earliestDate"];
	    }
	}
	export class Group {
	    name: string;
	    instruments: Instrument[];
	
	    static createFrom(source: any = {}) {
	        return new Group(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.instruments = this.convertValues(source["instruments"], Instrument);
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
	export class Catalog {
	    version: string;
	    groups: Group[];
	    timeframes: string[];
	
	    static createFrom(source: any = {}) {
	        return new Catalog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.groups = this.convertValues(source["groups"], Group);
	        this.timeframes = source["timeframes"];
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
	
	export class Job {
	    id: string;
	    symbol: string;
	    timeframe: string;
	    from: string;
	    to: string;
	    status: string;
	    progress: number;
	    message: string;
	    outputPath?: string;
	
	    static createFrom(source: any = {}) {
	        return new Job(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.symbol = source["symbol"];
	        this.timeframe = source["timeframe"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.status = source["status"];
	        this.progress = source["progress"];
	        this.message = source["message"];
	        this.outputPath = source["outputPath"];
	    }
	}
	export class Info {
	    directory: string;
	    runtimeAvailable: boolean;
	    runtimeError?: string;
	    error?: string;
	    jobs: Job[];
	    concurrency: number;
	    maxConcurrency: number;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.runtimeAvailable = source["runtimeAvailable"];
	        this.runtimeError = source["runtimeError"];
	        this.error = source["error"];
	        this.jobs = this.convertValues(source["jobs"], Job);
	        this.concurrency = source["concurrency"];
	        this.maxConcurrency = source["maxConcurrency"];
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
	
	
	export class Request {
	    symbols: string[];
	    timeframe: string;
	    from: string;
	    to: string;
	    concurrency?: number;
	
	    static createFrom(source: any = {}) {
	        return new Request(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.symbols = source["symbols"];
	        this.timeframe = source["timeframe"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.concurrency = source["concurrency"];
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

