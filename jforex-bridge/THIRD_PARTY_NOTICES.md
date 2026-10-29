CryptoDesk JForex bridge
=======================

The CryptoDesk bridge source and compiled cryptodesk-jforex.jar are distributed
under the application's Apache License 2.0 (LICENSE). This archive contains the
bridge and the SDK artifact manifest; it does not redistribute vendor SDK JARs.

JForex is a Dukascopy product. The JForex API source copyright notice states:
"Copyright 2009 Dukascopy® (Suisse) SA. All rights reserved.
DUKASCOPY PROPRIETARY/CONFIDENTIAL. Use is subject to license terms."
The SDK is not relicensed under CryptoDesk's license.

On first connection CryptoDesk downloads the unmodified, pinned SDK and its
runtime dependencies from Dukascopy's public Maven repository and Maven Central
to the user's private application profile. SDK-ARTIFACTS.json records each exact
version, origin URL, byte length and SHA-256. Subsequent use verifies cached files.
The original JAR contents and their embedded license/notice files are preserved.
Use of Dukascopy services and SDK remains subject to Dukascopy's applicable terms.

Official SDK and API:
https://www.dukascopy.com/swiss/english/forex/api/jforex-api/
https://www.dukascopy.com/client/jforexlib/publicrepo/
https://www.dukascopy.com/client/javadoc3/com/dukascopy/api/system/IClient.html

Java 17 or later must be installed separately. CryptoDesk does not redistribute
a Java runtime, install system packages, or require the JForex desktop platform.
