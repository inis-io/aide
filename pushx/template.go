package pushx

// TempEmailCode - 临时邮箱验证码脚本模板
const TempEmailCode = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <style type="text/css">
        body {
            font-family: 'Helvetica Neue', Arial, sans-serif;
            background-color: #f5f5f5;
            margin: 0;
            padding: 0;
            color: #333333;
            line-height: 1.6;
        }
        
        .container {
            max-width: 600px;
            margin: 20px auto;
            background-color: #ffffff;
            border-radius: 8px;
            font-size: 15px;
            box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
            overflow: hidden;
        }
        
        .header {
            background-color: #4a6bff;
            padding: 30px 20px;
            text-align: center;
            color: white;
        }
        
        .logo {
            font-size: 24px;
            font-weight: bold;
            margin-bottom: 10px;
        }
        
        .content {
            padding: 20px 15px;
        }
        .verification-code {
            background-color: #f8f9fa;
            border-left: 4px solid #4a6bff;
            padding: 15px;
            margin: 25px 0;
            font-size: 24px;
            font-weight: bold;
            color: #4a6bff;
            text-align: center;
            letter-spacing: 5px;
        }
        
        .footer {
            background-color: #f5f5f5;
            padding: 20px;
            text-align: center;
            font-size: 12px;
            color: #999999;
        }
        
        .button {
            display: inline-block;
            background-color: #4a6bff;
            color: white;
            text-decoration: none;
            padding: 12px 25px;
            border-radius: 4px;
            margin: 15px 0;
            font-weight: bold;
        }
        
        .note {
            font-size: 12px;
            color: #666666;
            margin-top: 30px;
        }
        
        .divider {
            border-top: 1px solid #eeeeee;
            margin: 20px 0;
        }
        
        @media only screen and (max-width: 600px) {
            .container {
                margin: 0;
                border-radius: 0;
            }
            
            .content {
                padding: 20px;
            }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <div class="logo">${title}</div>
            <div>${subject}</div>
        </div>
        
        <div class="content">
            <p>尊敬的 <strong>${username}</strong>，您好！</p>
            
            <p>感谢您使用我们的服务，您正在进行邮箱验证，请使用以下验证码完成验证：</p>
            
            <div class="verification-code">
                ${code}
            </div>
            
            <p>该验证码将在 <strong>${expired}分钟</strong> 后失效，请尽快使用。</p>
            
            <p>如果您并未请求此验证码，请忽略此邮件。</p>
            
            <div class="divider"></div>
            
            <div class="note">
                <p>此为系统自动发送的邮件，请勿直接回复。</p>
            </div>
        </div>
        
        <div class="footer">
            <p>联系邮箱：${email}</p>
            <p>通信地址：${address}</p>
            <p>© ${year} ${title}. 保留所有权利</p>
        </div>
    </div>
</body>
</html>`
