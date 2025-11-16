#!/usr/bin/env node

// 📊 Automated Analytics Test Runner
// Uses test-config.json to run comprehensive API tests

const fs = require("fs");
const https = require("http");
const { URL } = require("url");

// Colors for console output
const colors = {
  reset: "\x1b[0m",
  red: "\x1b[31m",
  green: "\x1b[32m",
  yellow: "\x1b[33m",
  blue: "\x1b[34m",
  magenta: "\x1b[35m",
  cyan: "\x1b[36m",
};

class AnalyticsTestRunner {
  constructor() {
    this.config = this.loadConfig();
    this.stats = {
      total: 0,
      passed: 0,
      failed: 0,
      suites: {},
    };
  }

  loadConfig() {
    try {
      const configData = fs.readFileSync("./test-config.json", "utf8");
      return JSON.parse(configData);
    } catch (error) {
      console.error(
        `${colors.red}❌ Failed to load test-config.json: ${error.message}${colors.reset}`
      );
      process.exit(1);
    }
  }

  async makeRequest(method, endpoint, data = null) {
    return new Promise((resolve, reject) => {
      const url = new URL(this.config.testConfig.baseUrl + endpoint);

      const options = {
        hostname: url.hostname,
        port: url.port || 80,
        path: url.pathname + url.search,
        method: method,
        headers: {
          "Content-Type": "application/json",
          "X-Account-ID": this.config.testConfig.accountId,
          "X-Project-ID": this.config.testConfig.projectId,
        },
      };

      if (data && method !== "GET") {
        const payload = typeof data === "string" ? data : JSON.stringify(data);
        options.headers["Content-Length"] = Buffer.byteLength(payload);
      }

      const req = https.request(options, (res) => {
        let responseData = "";

        res.on("data", (chunk) => {
          responseData += chunk;
        });

        res.on("end", () => {
          try {
            const parsedData = responseData ? JSON.parse(responseData) : {};
            resolve({
              status: res.statusCode,
              data: parsedData,
              raw: responseData,
            });
          } catch (e) {
            resolve({
              status: res.statusCode,
              data: responseData,
              raw: responseData,
            });
          }
        });
      });

      req.on("error", (error) => {
        reject(error);
      });

      // Set timeout
      req.setTimeout(this.config.testConfig.timeout || 5000, () => {
        req.destroy();
        reject(new Error("Request timeout"));
      });

      if (data && method !== "GET") {
        const payload = typeof data === "string" ? data : JSON.stringify(data);
        req.write(payload);
      }

      req.end();
    });
  }

  async runTest(test) {
    this.stats.total++;

    try {
      console.log(`   ${colors.cyan}🔍 ${test.name}${colors.reset}...`);

      const startTime = Date.now();
      const response = await this.makeRequest(
        test.method,
        test.endpoint,
        test.data
      );
      const duration = Date.now() - startTime;

      const expectedStatus = test.expectedStatus || 200;

      if (response.status === expectedStatus) {
        console.log(
          `     ${colors.green}✅ PASS${colors.reset} (${response.status}) - ${duration}ms`
        );
        this.stats.passed++;
        return true;
      } else {
        console.log(
          `     ${colors.red}❌ FAIL${colors.reset} (${response.status}, expected ${expectedStatus}) - ${duration}ms`
        );
        console.log(
          `     ${colors.red}Response: ${JSON.stringify(
            response.data
          ).substring(0, 100)}...${colors.reset}`
        );
        this.stats.failed++;
        return false;
      }
    } catch (error) {
      console.log(
        `     ${colors.red}❌ ERROR${colors.reset} - ${error.message}`
      );
      this.stats.failed++;
      return false;
    }
  }

  async runTestSuite(suiteName, suite) {
    console.log(`\n${colors.blue}=== ${suite.name} ===${colors.reset}`);

    this.stats.suites[suiteName] = { passed: 0, failed: 0 };

    for (const test of suite.tests) {
      const passed = await this.runTest(test);
      if (passed) {
        this.stats.suites[suiteName].passed++;
      } else {
        this.stats.suites[suiteName].failed++;
      }
    }
  }

  async generateBulkData() {
    console.log(
      `\n${colors.yellow}📊 Generating bulk test data...${colors.reset}`
    );

    const bulkConfig = this.config.bulkTestData;
    const events = [];

    for (let i = 0; i < bulkConfig.eventCount; i++) {
      const event = {
        event_type:
          bulkConfig.events[
            Math.floor(Math.random() * bulkConfig.events.length)
          ],
        user_id:
          bulkConfig.users[Math.floor(Math.random() * bulkConfig.users.length)],
        session_id: `bulk_session_${i}`,
        properties: {
          page_url:
            bulkConfig.pages[
              Math.floor(Math.random() * bulkConfig.pages.length)
            ],
          country:
            bulkConfig.countries[
              Math.floor(Math.random() * bulkConfig.countries.length)
            ],
          device:
            bulkConfig.devices[
              Math.floor(Math.random() * bulkConfig.devices.length)
            ],
          timestamp: new Date().toISOString(),
        },
      };

      try {
        await this.makeRequest("POST", "/api/v1/events", event);
      } catch (error) {
        console.log(
          `   ${colors.red}❌ Failed to send bulk event ${i}${colors.reset}`
        );
      }
    }

    console.log(
      `   ${colors.green}✅ Generated ${bulkConfig.eventCount} bulk events${colors.reset}`
    );
  }

  async runPerformanceTests() {
    console.log(`\n${colors.blue}=== Performance Tests ===${colors.reset}`);

    // Cache performance test
    const cacheTest = this.config.performanceTests.cacheTest;
    console.log(
      `   ${colors.cyan}🔄 Testing cache performance...${colors.reset}`
    );

    const times = [];
    for (let i = 0; i < cacheTest.iterations; i++) {
      const startTime = Date.now();
      await this.makeRequest("GET", cacheTest.endpoint);
      const duration = Date.now() - startTime;
      times.push(duration);
      console.log(`     Iteration ${i + 1}: ${duration}ms`);
    }

    const avgTime = times.reduce((a, b) => a + b) / times.length;
    const improvement = times[0] > times[times.length - 1];

    console.log(
      `   ${colors.cyan}Average response time: ${avgTime.toFixed(2)}ms${
        colors.reset
      }`
    );
    if (improvement) {
      console.log(
        `   ${colors.green}✅ Cache performance improved${colors.reset}`
      );
    } else {
      console.log(
        `   ${colors.yellow}⚠️  No cache improvement detected${colors.reset}`
      );
    }
  }

  printSummary() {
    console.log(`\n${colors.blue}=== TEST SUMMARY ===${colors.reset}`);
    console.log(`\n${colors.cyan}📊 Overall Results:${colors.reset}`);
    console.log(`   Total Tests: ${this.stats.total}`);
    console.log(
      `   ${colors.green}✅ Passed: ${this.stats.passed}${colors.reset}`
    );
    console.log(
      `   ${colors.red}❌ Failed: ${this.stats.failed}${colors.reset}`
    );

    const successRate = ((this.stats.passed / this.stats.total) * 100).toFixed(
      1
    );
    console.log(`   Success Rate: ${successRate}%`);

    console.log(`\n${colors.cyan}📋 Suite Breakdown:${colors.reset}`);
    Object.entries(this.stats.suites).forEach(([suite, stats]) => {
      const total = stats.passed + stats.failed;
      const rate = ((stats.passed / total) * 100).toFixed(1);
      console.log(`   ${suite}: ${stats.passed}/${total} (${rate}%)`);
    });

    if (this.stats.failed === 0) {
      console.log(
        `\n${colors.green}🎉 All tests passed! Analytics system is working perfectly.${colors.reset}`
      );
    } else {
      console.log(
        `\n${colors.red}⚠️  ${this.stats.failed} test(s) failed. Check the output above for details.${colors.reset}`
      );
    }
  }

  async run() {
    console.log(
      `${colors.blue}🧪 Mentiq Analytics Automated Test Suite${colors.reset}`
    );
    console.log(
      `${colors.cyan}📊 Base URL: ${this.config.testConfig.baseUrl}${colors.reset}`
    );
    console.log(
      `${colors.cyan}🏢 Account ID: ${this.config.testConfig.accountId}${colors.reset}`
    );
    console.log(
      `${colors.cyan}📁 Project ID: ${this.config.testConfig.projectId}${colors.reset}\n`
    );

    // Run all test suites
    for (const [suiteName, suite] of Object.entries(this.config.testSuites)) {
      await this.runTestSuite(suiteName, suite);
    }

    // Generate bulk data for analytics
    await this.generateBulkData();

    // Run performance tests
    await this.runPerformanceTests();

    // Print summary
    this.printSummary();

    // Exit with appropriate code
    process.exit(this.stats.failed === 0 ? 0 : 1);
  }
}

// Main execution
if (require.main === module) {
  const runner = new AnalyticsTestRunner();
  runner.run().catch((error) => {
    console.error(
      `${colors.red}❌ Test runner error: ${error.message}${colors.reset}`
    );
    process.exit(1);
  });
}

module.exports = AnalyticsTestRunner;
