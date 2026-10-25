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
	
	export class AnalysisBar {
	    index: number;
	    time: number;
	    values: Record<string, number>;

	    static createFrom(source: any = {}) {
	        return new AnalysisBar(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.time = source["time"];
	        this.values = source["values"];
	    }
	}
	export class DataFile {
	    id: string;
	    name: string;
	    symbol?: string;
	    timeframe?: string;
	    from?: string;
	    to?: string;
	    firstTimestamp?: number;
	    lastTimestamp?: number;
	    fields: string[];
	    bytes: number;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new DataFile(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.symbol = source["symbol"];
	        this.timeframe = source["timeframe"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.firstTimestamp = source["firstTimestamp"];
	        this.lastTimestamp = source["lastTimestamp"];
	        this.fields = source["fields"];
	        this.bytes = source["bytes"];
	        this.error = source["error"];
	    }
	}
	export class AnalysisCatalog {
	    directory: string;
	    fields: string[];
	    timeframes: string[];
	    files: DataFile[];

	    static createFrom(source: any = {}) {
	        return new AnalysisCatalog(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.fields = source["fields"];
	        this.timeframes = source["timeframes"];
	        this.files = this.convertValues(source["files"], DataFile);
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
	export class AnalysisChart {
	    resultId: string;
	    name: string;
	    symbol: string;
	    timeframe: string;
	    occurrence: number;
	    totalMatches: number;
	    matchTime: number;
	    matchIndex: number;
	    startIndex: number;
	    fields: string[];
	    bars: AnalysisBar[];

	    static createFrom(source: any = {}) {
	        return new AnalysisChart(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.resultId = source["resultId"];
	        this.name = source["name"];
	        this.symbol = source["symbol"];
	        this.timeframe = source["timeframe"];
	        this.occurrence = source["occurrence"];
	        this.totalMatches = source["totalMatches"];
	        this.matchTime = source["matchTime"];
	        this.matchIndex = source["matchIndex"];
	        this.startIndex = source["startIndex"];
	        this.fields = source["fields"];
	        this.bars = this.convertValues(source["bars"], AnalysisBar);
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
	export class AnalysisOperand {
	    kind: string;
	    field: string;
	    value: number;
	    timeframe: string;
	    offset: number;
	    aggregate: string;
	    window: number;

	    static createFrom(source: any = {}) {
	        return new AnalysisOperand(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.field = source["field"];
	        this.value = source["value"];
	        this.timeframe = source["timeframe"];
	        this.offset = source["offset"];
	        this.aggregate = source["aggregate"];
	        this.window = source["window"];
	    }
	}
	export class AnalysisCondition {
	    id: string;
	    operator: string;
	    comparison: string;
	    left: AnalysisOperand;
	    right: AnalysisOperand;
	    upper: AnalysisOperand;
	    distance: AnalysisOperand;
	    multiplier: number;
	    bars: number;

	    static createFrom(source: any = {}) {
	        return new AnalysisCondition(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.operator = source["operator"];
	        this.comparison = source["comparison"];
	        this.left = this.convertValues(source["left"], AnalysisOperand);
	        this.right = this.convertValues(source["right"], AnalysisOperand);
	        this.upper = this.convertValues(source["upper"], AnalysisOperand);
	        this.distance = this.convertValues(source["distance"], AnalysisOperand);
	        this.multiplier = source["multiplier"];
	        this.bars = source["bars"];
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
	export class AnalysisResult {
	    id: string;
	    fileId: string;
	    name: string;
	    symbol: string;
	    timeframe: string;
	    status: string;
	    error?: string;
	    rows: number;
	    matches: number;
	    assetMatches: number;
	    assetFiles: number;
	    firstTime?: number;
	    lastTime?: number;

	    static createFrom(source: any = {}) {
	        return new AnalysisResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.fileId = source["fileId"];
	        this.name = source["name"];
	        this.symbol = source["symbol"];
	        this.timeframe = source["timeframe"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.rows = source["rows"];
	        this.matches = source["matches"];
	        this.assetMatches = source["assetMatches"];
	        this.assetFiles = source["assetFiles"];
	        this.firstTime = source["firstTime"];
	        this.lastTime = source["lastTime"];
	    }
	}
	export class AnalysisInfo {
	    active: boolean;
	    phase: string;
	    total: number;
	    completed: number;
	    failed: number;
	    currentFile: string;
	    progress: number;
	    rows: number;
	    totalMatches: number;
	    matchedFiles: number;
	    results: AnalysisResult[];
	    offset: number;
	    limit: number;
	    totalResults: number;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new AnalysisInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.active = source["active"];
	        this.phase = source["phase"];
	        this.total = source["total"];
	        this.completed = source["completed"];
	        this.failed = source["failed"];
	        this.currentFile = source["currentFile"];
	        this.progress = source["progress"];
	        this.rows = source["rows"];
	        this.totalMatches = source["totalMatches"];
	        this.matchedFiles = source["matchedFiles"];
	        this.results = this.convertValues(source["results"], AnalysisResult);
	        this.offset = source["offset"];
	        this.limit = source["limit"];
	        this.totalResults = source["totalResults"];
	        this.error = source["error"];
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

	export class AnalysisRequest {
	    conditions: AnalysisCondition[];

	    static createFrom(source: any = {}) {
	        return new AnalysisRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conditions = this.convertValues(source["conditions"], AnalysisCondition);
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

	export class EnrichmentJob {
	    id: string;
	    fileId: string;
	    name: string;
	    status: string;
	    progress: number;
	    rows: number;
	    message: string;
	    columns: string[];
	    backupPath?: string;

	    static createFrom(source: any = {}) {
	        return new EnrichmentJob(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.fileId = source["fileId"];
	        this.name = source["name"];
	        this.status = source["status"];
	        this.progress = source["progress"];
	        this.rows = source["rows"];
	        this.message = source["message"];
	        this.columns = source["columns"];
	        this.backupPath = source["backupPath"];
	    }
	}
	export class EnrichmentInfo {
	    directory: string;
	    active: boolean;
	    runtimeAvailable: boolean;
	    runtimeError?: string;
	    indicator: string;
	    total: number;
	    completed: number;
	    failed: number;
	    cancelled: number;
	    currentFile: string;
	    currentProgress: number;
	    jobs: EnrichmentJob[];

	    static createFrom(source: any = {}) {
	        return new EnrichmentInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.active = source["active"];
	        this.runtimeAvailable = source["runtimeAvailable"];
	        this.runtimeError = source["runtimeError"];
	        this.indicator = source["indicator"];
	        this.total = source["total"];
	        this.completed = source["completed"];
	        this.failed = source["failed"];
	        this.cancelled = source["cancelled"];
	        this.currentFile = source["currentFile"];
	        this.currentProgress = source["currentProgress"];
	        this.jobs = this.convertValues(source["jobs"], EnrichmentJob);
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

	export class EnrichmentRequest {
	    indicator: string;
	    parameters: Record<string, number>;
	    source: string;

	    static createFrom(source: any = {}) {
	        return new EnrichmentRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.indicator = source["indicator"];
	        this.parameters = source["parameters"];
	        this.source = source["source"];
	    }
	}
	export class FilePage {
	    directory: string;
	    files: DataFile[];
	    total: number;
	    offset: number;
	    limit: number;

	    static createFrom(source: any = {}) {
	        return new FilePage(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.files = this.convertValues(source["files"], DataFile);
	        this.total = source["total"];
	        this.offset = source["offset"];
	        this.limit = source["limit"];
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

	export class IndicatorParameter {
	    key: string;
	    label: string;
	    default: number;
	    min: number;
	    max: number;
	    step: number;

	    static createFrom(source: any = {}) {
	        return new IndicatorParameter(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.default = source["default"];
	        this.min = source["min"];
	        this.max = source["max"];
	        this.step = source["step"];
	    }
	}
	export class Indicator {
	    id: string;
	    name: string;
	    category: string;
	    input: string;
	    parameters: IndicatorParameter[];
	    note?: string;

	    static createFrom(source: any = {}) {
	        return new Indicator(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.category = source["category"];
	        this.input = source["input"];
	        this.parameters = this.convertValues(source["parameters"], IndicatorParameter);
	        this.note = source["note"];
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
	export class IndicatorCatalog {
	    version: string;
	    indicators: Indicator[];

	    static createFrom(source: any = {}) {
	        return new IndicatorCatalog(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.indicators = this.convertValues(source["indicators"], Indicator);
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
	    active: boolean;
	
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
	        this.active = source["active"];
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
	
	
	export class RefreshJob {
	    id: string;
	    fileId: string;
	    name: string;
	    status: string;
	    progress: number;
	    rows: number;
	    addedRows: number;
	    updatedRows: number;
	    message: string;
	    columns: string[];
	    backupPath?: string;

	    static createFrom(source: any = {}) {
	        return new RefreshJob(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.fileId = source["fileId"];
	        this.name = source["name"];
	        this.status = source["status"];
	        this.progress = source["progress"];
	        this.rows = source["rows"];
	        this.addedRows = source["addedRows"];
	        this.updatedRows = source["updatedRows"];
	        this.message = source["message"];
	        this.columns = source["columns"];
	        this.backupPath = source["backupPath"];
	    }
	}
	export class RefreshInfo {
	    directory: string;
	    active: boolean;
	    runtimeAvailable: boolean;
	    runtimeError?: string;
	    total: number;
	    completed: number;
	    failed: number;
	    cancelled: number;
	    currentFile: string;
	    currentProgress: number;
	    jobs: RefreshJob[];

	    static createFrom(source: any = {}) {
	        return new RefreshInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.active = source["active"];
	        this.runtimeAvailable = source["runtimeAvailable"];
	        this.runtimeError = source["runtimeError"];
	        this.total = source["total"];
	        this.completed = source["completed"];
	        this.failed = source["failed"];
	        this.cancelled = source["cancelled"];
	        this.currentFile = source["currentFile"];
	        this.currentProgress = source["currentProgress"];
	        this.jobs = this.convertValues(source["jobs"], RefreshJob);
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
	export class ViewState {
	    symbols: string[];
	    timeframe: string;
	    from: string;
	    to: string;
	    concurrency: number;
	    collapsedCategories: string[];

	    static createFrom(source: any = {}) {
	        return new ViewState(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.symbols = source["symbols"];
	        this.timeframe = source["timeframe"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.concurrency = source["concurrency"];
	        this.collapsedCategories = source["collapsedCategories"];
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

