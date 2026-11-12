declare module '@usebruno/lang' {
  export function bruToJsonV2(content: string): any;
  export function jsonToBruV2(json: any): string;
  export function bruToEnvJsonV2(content: string): any;
  export function envJsonToBruV2(json: any): string;
  export function collectionBruToJson(content: string): any;
  export function jsonToCollectionBru(json: any): string;
}

declare module 'neo-blessed' {
  import * as blessed from 'blessed';
  export = blessed;
}
