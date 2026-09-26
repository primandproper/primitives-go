/*
Package nullable converts between Go values and the database/sql Null types.

It imports nothing outside the standard library, which is the reason it is a
package of its own: filtering needs these conversions, and observability/tracing
imports filtering, so reaching them through database made every instrumented
package in the module depend on database. The same functions remain in
database, delegating here.
*/
package nullable
